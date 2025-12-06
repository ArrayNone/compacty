package compressor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ArrayNone/compacty/internal/config"
	"github.com/ArrayNone/compacty/internal/maputils"
	"github.com/ArrayNone/compacty/internal/textutils"
	"github.com/ArrayNone/compacty/internal/prints"

	"github.com/fatih/color"
)

type WriteMode int

const (
	KeepBest  WriteMode = iota // Copies the best compressed file onto the same directory as the input file
	KeepAll                    // Copy all compressed files onto the same directory as the input file
	Overwrite                  // Overwrites the input file with the best compressed file
	None                       // Do not produce a new file even if the file is successfully compressed
)

type TempFile struct {
	Path        string
	CreateError error
}

type ExecutedTool struct {
	*config.CompressionTool
	Arguments []string
}

type FileInfo struct {
	Path string

	Directory string
	FileName  string
	BaseName  string
	Extension string

	Size int64

	Decode DecodeTimeBench
}

type CompressionResult struct {
	Command   *exec.Cmd
	Arguments []string

	TimeTaken time.Duration

	OriginalSize int64
	FinalSize    int64

	CreateError        error
	CommandError       error
	ReadFinalSizeError error

	Decode DecodeTimeBench
}

type CompressionProcess struct {
	OriginalFileInfo []*FileInfo
	OriginalPaths    []string
	TempFiles        map[string][]TempFile

	Results  map[string][]*CompressionResult
	Wrappers map[string]string

	MinDecodeTime         time.Duration
	AreDecodeTimeComputed bool

	toolOutput io.Writer
}

type compressionCommand struct {
	command    *exec.Cmd
	tool       ExecutedTool
	stdoutFile *os.File

	toolName   string
	arguments  []string
	inputPaths []string
	wrapper    string

	timeTaken time.Duration

	commandError error
	isAvailable  bool
}

func NewCompressionProcess(
	paths []string,
	wrappers map[string]string,
	toolOutput io.Writer,
) (c *CompressionProcess, allOk bool) {

	allOk = true

	originalFileInfo := make([]*FileInfo, 0, len(paths))
	originalPaths := make([]string, 0, len(paths))

	for _, path := range paths {
		fileInfo, err := getFileInfo(path)
		if err != nil {
			prints.Warnf("Cannot compress %s: %v. Skipping...\n", path, err)
			allOk = false
			continue
		}

		originalFileInfo = append(originalFileInfo, &fileInfo)
		originalPaths = append(originalPaths, path)
	}

	return &CompressionProcess{
		OriginalFileInfo: originalFileInfo,
		OriginalPaths:    originalPaths,
		TempFiles:        make(map[string][]TempFile),

		Results:  make(map[string][]*CompressionResult),
		Wrappers: wrappers,

		AreDecodeTimeComputed: false,

		toolOutput: toolOutput,
	}, allOk
}

func (c *CompressionProcess) CompressSingle(fileIdx int, ctx context.Context, tools map[string]ExecutedTool) (done chan struct{}) {
	commands := make(map[string]*compressionCommand)
	commandListBuilder := &strings.Builder{}

	for name, tool := range tools {
		if _, ok := c.Results[name]; !ok {
			c.Results[name] = make([]*CompressionResult, len(c.OriginalFileInfo))
		}

		if _, ok := c.TempFiles[name]; !ok {
			c.TempFiles[name] = make([]TempFile, len(c.OriginalFileInfo))
		}

		wrapper := config.QueryWrapper(c.Wrappers, tool.Platform, runtime.GOOS)

		command := newCompressionCommand(name, tool, wrapper)
		commands[name] = command

		c.TempFiles[name][fileIdx] = command.prepareSingleTempFile(c.OriginalFileInfo[fileIdx])
		command.prepareCommand(ctx)

		command.writeCommandLine(commandListBuilder)
	}

	prints.Print(commandListBuilder.String())

	done = make(chan struct{})

	go func(c *CompressionProcess, done chan struct{}) {
		var (
			waitGroup sync.WaitGroup
			mutex     sync.Mutex
		)

		for _, command := range commands {
			waitGroup.Add(1)

			command.setStdoutAndErr(c.toolOutput)

			go func(c *CompressionProcess, command *compressionCommand, wg *sync.WaitGroup, mut *sync.Mutex, i int) {
				defer wg.Done()
				command.executeAndReport()

				result := command.generateSingleResult(
					c.OriginalFileInfo[i],
					c.TempFiles[command.toolName][i],
				)

				mut.Lock()
				c.Results[command.toolName][i] = result
				mut.Unlock()
			}(c, command, &waitGroup, &mutex, fileIdx)
		}

		waitGroup.Wait()
		done <- struct{}{}
	}(c, done)

	return done
}

func (c *CompressionProcess) CompressAll(ctx context.Context, tools map[string]ExecutedTool) (done chan struct{}) {
	commands := make(map[string]*compressionCommand)
	commandListBuilder := &strings.Builder{}

	for name, tool := range tools {
		if !tool.CanBatchCompress() {
			prints.Warnf("Compressing all files at once requires tools to be able to batch compress, which %s don't do. Skipping...\n", name)
			continue
		}

		wrapper := config.QueryWrapper(c.Wrappers, tool.Platform, runtime.GOOS)

		command := newCompressionCommand(name, tool, wrapper)
		commands[name] = command

		c.TempFiles[name] = command.prepareTempFiles(c.OriginalFileInfo)
		command.prepareCommand(ctx)

		command.writeCommandLine(commandListBuilder)
	}

	prints.Print(commandListBuilder.String())

	done = make(chan struct{})

	go func(c *CompressionProcess, done chan struct{}) {
		var (
			waitGroup sync.WaitGroup
			mutex     sync.Mutex
		)

		for _, command := range commands {
			waitGroup.Add(1)

			command.setStdoutAndErr(c.toolOutput)

			go func(c *CompressionProcess, command *compressionCommand, wg *sync.WaitGroup, mut *sync.Mutex) {
				defer wg.Done()
				command.executeAndReport()

				tempFiles := c.TempFiles[command.toolName]
				results := command.generateResults(c.OriginalFileInfo, tempFiles)

				mut.Lock()
				c.Results[command.toolName] = results
				mut.Unlock()
			}(c, command, &waitGroup, &mutex)
		}

		waitGroup.Wait()
		done <- struct{}{}
	}(c, done)

	return done
}

func (c *CompressionProcess) BenchmarkDecodeTime(minTime time.Duration) (done chan struct{}) {
	c.AreDecodeTimeComputed = true
	c.MinDecodeTime = minTime

	done = make(chan struct{})

	go func(c *CompressionProcess, done chan struct{}) {
		totalFiles := len(c.OriginalFileInfo)

		fileText := textutils.PluralNoun(totalFiles, "files", "file")
		prints.Println(color.BlueString("Computing decode time for %d %s:", totalFiles, fileText))

		for i, file := range c.OriginalFileInfo {
			prints.Printf("%s %s\n", file.Path, color.CyanString("(%d/%d)", i+1, totalFiles))

			file.Decode = benchDecodeTime(file.Path, minTime)
			if file.Decode.Err != nil {
				prints.Warnf("Error occurred while benchmarking %s: %v\n", file.Path, file.Decode.Err)
			}

			for toolName, results := range c.Results {
				result := results[i]

				tempFile := c.TempFiles[toolName][i]

				if tempFile.CreateError != nil {
					result.Decode = DecodeTimeBench{
						Total:   file.Decode.Total,
						Average: file.Decode.Average,
						Trials:  file.Decode.Trials,
						Err:     tempFile.CreateError,
					}

					prints.Warnf("Cannot benchmark %s on %s. Output file doesn't exist: %v\n",
						toolName,
						file.FileName,
						tempFile.CreateError,
					)

					continue
				}

				result.Decode = benchDecodeTime(tempFile.Path, minTime)
				if result.Decode.Err != nil {
					prints.Warnf("Error occurred while benchmarking %s on %s: %v\n", tempFile.Path, toolName, result.Decode.Err)
				}
			}
		}

		prints.Println()
		done <- struct{}{}
	}(c, done)

	return done
}

func (c *CompressionProcess) SaveResultsAndReport(writeMode WriteMode) (allOk bool) {
	allOk = true

	sortedToolNames := maputils.SortedKeys(c.Results)

	prints.Println(color.BlueString("SUMMARY:"))

	for i := range c.OriginalFileInfo {
		bestToolSize := c.findBestToolSize(i)
		bestToolDecodeTime := c.findBestToolDecodeTime(i)

		c.printResultSummary(i, bestToolSize, bestToolDecodeTime, sortedToolNames)
		ok := c.flushResult(bestToolSize, i, writeMode)
		if !ok {
			allOk = false
		}

		prints.Println()
	}

	return allOk
}

func (c *CompressionProcess) CleanUp() {
	for _, tempFiles := range c.TempFiles {
		for _, tempFile := range tempFiles {
			_ = os.Remove(tempFile.Path)
		}
	}
}

func (c *CompressionProcess) IsErrorFree() (ok bool) {
	for _, toolResults := range c.Results {
		for _, result := range toolResults {
			if result.HasError() {
				return false
			}
		}
	}

	return true
}

func ToolConfigToExecutedTool(tool *config.ToolConfig, argPreset string) (result ExecutedTool, ok bool) {
	args, ok := tool.Arguments[argPreset]
	if !ok {
		return ExecutedTool{}, false
	}

	return ExecutedTool{
		CompressionTool: &tool.CompressionTool,
		Arguments:       args,
	}, true
}

func (r *CompressionResult) HasError() bool {
	return r.CommandError != nil || r.ReadFinalSizeError != nil || r.Decode.Err != nil || r.CreateError != nil
}

func (c *CompressionProcess) findBestToolSize(fileIdx int) (bestTool string) {
	bestSize := c.OriginalFileInfo[fileIdx].Size

	for toolName, toolResults := range c.Results {
		result := toolResults[fileIdx]
		if result.HasError() {
			continue
		}

		if result.FinalSize < bestSize {
			bestSize = result.FinalSize
			bestTool = toolName
		}
	}

	return bestTool
}

func (c *CompressionProcess) findBestToolDecodeTime(fileIdx int) (bestTool string) {
	bestDecodeTime := c.OriginalFileInfo[fileIdx].Decode.Average

	for toolName, toolResults := range c.Results {
		result := toolResults[fileIdx]
		if result.Decode.Average < bestDecodeTime {
			bestDecodeTime = result.Decode.Average
			bestTool = toolName
		}
	}

	return bestTool
}

func (c *CompressionProcess) flushResult(fromTool string, fileIdx int, writeMode WriteMode) (ok bool) {
	ok = true

	fileInfo := c.OriginalFileInfo[fileIdx]

	switch writeMode {
	case None:
		return ok
	case KeepAll:
		for toolName := range c.Results {
			tempFile := c.TempFiles[toolName][fileIdx]
			resultPath := compressedFilePath(fileInfo.Directory, fileInfo.BaseName, toolName, fileInfo.Extension)

			err := moveFile(tempFile.Path, resultPath)
			if err != nil {
				prints.Warnf("Cannot move result %s to %s: %v\n", tempFile.Path, resultPath, err)
				ok = false
			} else {
				prints.Printf("Successfully moved result %s to %s.\n", tempFile.Path, color.CyanString(resultPath))
			}
		}

		return ok
	}

	if fromTool == "" {
		prints.Println("File cannot be compressed further. The original file is left as is.")
		return ok
	}

	bestTempFile := c.TempFiles[fromTool][fileIdx]
	bestTempPath := bestTempFile.Path

	switch writeMode {
	case KeepBest:
		resultPath := compressedFilePath(fileInfo.Directory, fileInfo.BaseName, fromTool, fileInfo.Extension)

		err := moveFile(bestTempPath, resultPath)
		if err != nil {
			prints.Warnf("Cannot move result %s to %s: %v\n", bestTempPath, resultPath, err)
			ok = false
		} else {
			prints.Printf("%s wins! Successfully moved result %s to %s.\n", fromTool, bestTempPath, color.CyanString(resultPath))
		}
	case Overwrite:
		err := moveFile(bestTempPath, fileInfo.Path)
		if err != nil {
			prints.Warnf("Cannot overwrite %s: %v\n", fileInfo.Path, err)
			ok = false
		} else {
			prints.Printf("%s wins! Successfully overwritten %s.\n", fromTool, color.CyanString(fileInfo.Path))
		}
	}

	return ok
}

func (c *CompressionProcess) printResultSummary(fileIdx int, bestToolSize, bestToolDecodeTime string, presortedToolNames []string) {
	fileInfo := c.OriginalFileInfo[fileIdx]

	summaryBuilder := &strings.Builder{}

	summaryBuilder.WriteString(fileInfo.Path)
	summaryBuilder.WriteString(" | ")
	summaryBuilder.WriteString(color.CyanString("Size (B)"))
	if c.AreDecodeTimeComputed {
		summaryBuilder.WriteString(" - ")
		summaryBuilder.WriteString(color.CyanString("Decode Time (ms avg within %v, w/ Go's native libraries)", c.MinDecodeTime))
	}

	summaryBuilder.WriteByte('\n')

	summaryBuilder.WriteString("| original: ")
	summaryBuilder.WriteString(color.CyanString("%d (100.000000%%)", fileInfo.Size))

	if c.AreDecodeTimeComputed {
		summaryBuilder.WriteString(" - ")

		if fileInfo.Decode.Err != nil {
			summaryBuilder.WriteString(color.YellowString("DECODE TIME ERROR"))
		} else {
			summaryBuilder.WriteString(color.CyanString(fileInfo.Decode.MSAverageWithTrialsCountString()))
		}
	}

	summaryBuilder.WriteByte('\n')

	for _, toolName := range presortedToolNames {
		toolResult := c.Results[toolName][fileIdx]

		summaryBuilder.WriteString("| " + toolName + ": ")

		if toolResult.CreateError != nil {
			summaryBuilder.WriteString(color.YellowString("CANNOT CREATE OUTPUT FILE"))
			summaryBuilder.WriteByte('\n') // Coloured \n messes up spacing, must be separated
			continue
		}

		if toolResult.CommandError != nil {
			summaryBuilder.WriteString(color.YellowString("COMPRESSION FAILED DUE TO ERROR"))
			summaryBuilder.WriteByte('\n') // Coloured \n messes up spacing, must be separated
			continue
		}

		writeSizeLine(summaryBuilder, toolResult, toolName == bestToolSize)

		if c.AreDecodeTimeComputed {
			summaryBuilder.WriteString(" - ")
			writeDecodeTime(summaryBuilder, toolResult, fileInfo.Decode, toolName == bestToolDecodeTime)
		}

		summaryBuilder.WriteByte('\n')
	}

	prints.Print(summaryBuilder.String())
}

func writeSizeLine(summaryBuilder *strings.Builder, result *CompressionResult, isBest bool) {
	if result.ReadFinalSizeError != nil {
		summaryBuilder.WriteString(color.YellowString("FILE SIZE ERROR"))
		return
	}

	percentage := float32(result.FinalSize) * 100 / float32(result.OriginalSize)
	sizeLine := fmt.Sprintf("%d (%f%%)", result.FinalSize, percentage)
	if isBest {
		sizeLine = color.GreenString(sizeLine)
	} else if result.FinalSize > result.OriginalSize {
		sizeLine = color.YellowString(sizeLine)
	} else {
		sizeLine = color.CyanString(sizeLine)
	}

	summaryBuilder.WriteString(sizeLine)
}

func writeDecodeTime(summaryBuilder *strings.Builder, result *CompressionResult, originalDecode DecodeTimeBench, isBest bool) {
	if result.Decode.Err != nil {
		summaryBuilder.WriteString(color.YellowString("DECODE TIME ERROR"))
		return
	}

	decodeTimeLine := result.Decode.MSAverageWithTrialsCountString()
	if isBest {
		decodeTimeLine = color.GreenString(decodeTimeLine)
	} else if result.Decode.Average > originalDecode.Average {
		decodeTimeLine = color.YellowString(decodeTimeLine)
	} else {
		decodeTimeLine = color.CyanString(decodeTimeLine)
	}

	summaryBuilder.WriteString(decodeTimeLine)
}

func newCompressionCommand(toolName string, tool ExecutedTool, wrapper string) (cc *compressionCommand) {
	return &compressionCommand{
		toolName: toolName,

		tool: tool,

		wrapper:   wrapper,
		arguments: tool.Arguments,

		timeTaken: 0,

		commandError: nil,
		isAvailable:  false,
	}
}

func (cc *compressionCommand) prepareSingleTempFile(fileInfo *FileInfo) (tempFile TempFile) {
	tempPath := compressedFilePath(os.TempDir(), fileInfo.BaseName, cc.toolName, fileInfo.Extension)

	if cc.tool.OutputMode == config.Stdout {
		file, err := os.Create(tempPath)
		if err != nil {
			prints.Warnf("Failed to create temp file %s. Skipping...\n", tempPath)

			cc.inputPaths = []string{}
			return TempFile{
				Path:        tempPath,
				CreateError: err,
			}
		}

		cc.stdoutFile = file
		cc.inputPaths = []string{fileInfo.Path}
	} else if cc.tool.Overwrites() {
		err := copyFileTo(fileInfo.Path, tempPath)
		if err != nil {
			prints.Warnf("Failed to create temp file %s. Skipping...\n", tempPath)

			cc.inputPaths = []string{}
			return TempFile{
				Path:        tempPath,
				CreateError: err,
			}
		}

		cc.inputPaths = []string{tempPath}
	} else {
		cc.inputPaths = []string{fileInfo.Path, tempPath}
	}

	return TempFile{
		Path:        tempPath,
		CreateError: nil,
	}
}

func (cc *compressionCommand) prepareTempFiles(fileInfo []*FileInfo) (tempFiles []TempFile) {
	tempFiles = make([]TempFile, len(fileInfo))
	cc.inputPaths = make([]string, 0, len(fileInfo))

	for i, file := range fileInfo {
		tempPath := compressedFilePath(os.TempDir(), file.BaseName, cc.toolName, file.Extension)
		err := copyFileTo(file.Path, tempPath)

		tempFiles[i] = TempFile{
			Path:        tempPath,
			CreateError: err,
		}

		if err != nil {
			prints.Warnf("Failed to create temp file %s. Skipping...\n", tempPath)
			continue
		}

		cc.inputPaths = append(cc.inputPaths, tempPath)
	}

	return tempFiles
}

func (cc *compressionCommand) writeCommandLine(commandListBuilder *strings.Builder) {
	commandListBuilder.WriteString("| ")

	binPath := cc.command.Args[0]
	commandListBuilder.WriteString(filepath.Base(binPath))

	if cc.wrapper != "" {
		execPath := cc.command.Args[1]

		commandListBuilder.WriteByte(' ')
		commandListBuilder.WriteString(execPath)
	}

	commandListBuilder.WriteByte(' ')

	if len(cc.arguments) > 0 {
		commandListBuilder.WriteString(strings.Join(cc.arguments, " "))
		commandListBuilder.WriteByte(' ')
	}

	const maxPathPrinted = 5 // Do not spam output
	if len(cc.inputPaths) <= maxPathPrinted {
		pathString := strings.Join(cc.inputPaths, " ")

		if cc.stdoutFile != nil {
			stdoutName := cc.stdoutFile.Name()
			commandListBuilder.WriteString(color.CyanString(pathString + " > " + stdoutName))
		} else {
			commandListBuilder.WriteString(color.CyanString(pathString))
		}
	} else {
		commandListBuilder.WriteString(color.CyanString("..."))
	}

	commandListBuilder.WriteByte('\n')
}

func (cc *compressionCommand) prepareCommand(ctx context.Context) {
	var commandString string
	usedArgs := make([]string, 0, len(cc.tool.Arguments)+len(cc.inputPaths))

	commandString, ok := config.FindExecutablePath(cc.tool.Command, cc.tool.Platform)
	if !ok {
		return
	}

	if !slices.Contains(cc.tool.Platform, runtime.GOOS) {
		usedArgs = append(usedArgs, commandString)
		commandString = cc.wrapper
	}

	usedArgs = append(usedArgs, cc.tool.Arguments...)
	usedArgs = append(usedArgs, cc.inputPaths...)

	cc.command = exec.CommandContext(ctx, commandString, usedArgs...)
	if cc.stdoutFile != nil {
		cc.command.Stdout = cc.stdoutFile
	}

	cc.isAvailable = true
}

func (cc *compressionCommand) setStdoutAndErr(writer io.Writer) {
	_, isOsFile := cc.command.Stdout.(*os.File)
	if !isOsFile {
		cc.command.Stdout = writer
	}

	cc.command.Stderr = writer
}

var errCmdNotFound = errors.New("tool not found")
var errNoInput = errors.New("no input given")

func (cc *compressionCommand) executeAndReport() {
	if !cc.isAvailable {
		cc.commandError = errCmdNotFound

		prints.Warnf("Cannot start %s. No executable available.\n", cc.toolName)
		return
	}

	if len(cc.inputPaths) == 0 {
		cc.commandError = errNoInput

		prints.Warnf("Cannot start %s. No input files are given.\n", cc.toolName)
		return
	}

	start := time.Now()
	err := cc.command.Run()

	cc.timeTaken = time.Since(start)

	if err != nil {
		cc.commandError = err

		prints.Warnf("%s errored in %s: %v\n", cc.toolName, cc.timeTaken.String(), err)
		return
	} else if cc.stdoutFile != nil {
		err := cc.stdoutFile.Close()
		if err != nil {
			cc.commandError = err

			prints.Warnf("%s errored in %s: failed to close output due to %v\n", cc.toolName, cc.timeTaken.String(), err)
			return
		}
	}

	prints.Println(cc.toolName, "finished in", cc.timeTaken.String())
}

func (cc *compressionCommand) generateSingleResult(originalFileInfo *FileInfo, tempFile TempFile) *CompressionResult {
	if !cc.isAvailable {
		return cc.generateErrorResult(errCmdNotFound, originalFileInfo, tempFile)
	}

	if tempFile.CreateError != nil {
		return &CompressionResult{
			Command:   cc.command,
			Arguments: cc.arguments,

			FinalSize:    originalFileInfo.Size,
			OriginalSize: originalFileInfo.Size,

			TimeTaken: cc.timeTaken,

			CommandError:       cc.commandError,
			ReadFinalSizeError: nil,
			CreateError:        tempFile.CreateError,
		}
	}

	finalSize, errSize := getFileSize(tempFile.Path)
	return &CompressionResult{
		Command:   cc.command,
		Arguments: cc.tool.Arguments,

		FinalSize:    finalSize,
		OriginalSize: originalFileInfo.Size,

		TimeTaken: cc.timeTaken,

		CommandError:       cc.commandError,
		ReadFinalSizeError: errSize,
		// Decode time-related stuff are computed later
	}
}

func (cc *compressionCommand) generateResults(originalFileInfo []*FileInfo, tempFiles []TempFile) []*CompressionResult {
	results := make([]*CompressionResult, len(tempFiles))
	for i, fileInfo := range originalFileInfo {
		results[i] = cc.generateSingleResult(fileInfo, tempFiles[i])
	}

	return results
}

func (cc *compressionCommand) generateErrorResult(err error, originalFileInfo *FileInfo, tempFile TempFile) *CompressionResult {
	return &CompressionResult{
		Command:   cc.command,
		Arguments: cc.tool.Arguments,

		FinalSize:    originalFileInfo.Size,
		OriginalSize: originalFileInfo.Size,

		TimeTaken: cc.timeTaken,

		CommandError:       err,
		ReadFinalSizeError: nil,
		CreateError:        tempFile.CreateError,
		// Decode time-related stuff are computed later
	}
}

func compressedFilePath(dir, baseName, toolName, extension string) string {
	fileName := baseName + "-" + toolName + extension
	return filepath.Join(dir, fileName)
}

func getFileInfo(path string) (FileInfo, error) {
	directory, fileName := filepath.Split(path)
	extension := filepath.Ext(fileName)
	originalSize, err := getFileSize(path)

	if err != nil {
		return FileInfo{}, err
	}

	return FileInfo{
		Path: path,

		Directory: directory,
		FileName:  fileName,
		BaseName:  strings.TrimSuffix(fileName, extension),
		Extension: extension,

		Size: originalSize,

		// Keep decode time optional
	}, nil
}

func getFileSize(path string) (size int64, err error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return 0, err
	}

	return fileInfo.Size(), nil
}

func copyFileTo(pathSrc, pathDest string) (err error) {
	fileSource, err := os.Open(pathSrc)
	if err != nil {
		return err
	}
	defer fileSource.Close()

	fileDestination, err := os.Create(pathDest)
	if err != nil {
		return err
	}
	defer fileDestination.Close()

	_, err = io.Copy(fileDestination, fileSource)
	return err
}

func moveFile(pathSrc, pathDest string) error {
	err := os.Rename(pathSrc, pathDest)
	if err == nil {
		return nil
	}

	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		// Assume cross-device error or something that requires a fallback, try again
		if err := copyFileTo(pathSrc, pathDest); err != nil {
			return err
		}

		return os.Remove(pathSrc)
	}

	return err
}
