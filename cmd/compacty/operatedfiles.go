package main

import (
	"bufio"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/ArrayNone/compacty/internal/compressor"
	"github.com/ArrayNone/compacty/internal/config"
	"github.com/ArrayNone/compacty/internal/report"

	"github.com/gabriel-vasile/mimetype"
)

type RenameMode = int

type OperatedFiles struct {
	Paths     []string
	Extension string
	Mime      string

	PerFileTools   map[string]compressor.ExecutedTool
	BatchableTools map[string]compressor.ExecutedTool

	WarnLog  *log.Logger
	PrintLog *log.Logger
}

const (
	PromptUser RenameMode = iota
	ForceAccept
	ForceDecline
)

func PathsToOperatedFiles(cfg *config.Config, paths []string, renameMode RenameMode, printLog, warnLog *log.Logger) (operations []*OperatedFiles) {
	pathCollection := make(map[string][]string)

	fileFormats := cfg.GetSupportedFileFormats()
	fileExtensions := cfg.GetSupportedFileExtensions()

	for _, path := range paths {
		mime, err := mimetype.DetectFile(path)
		if err != nil {
			warnLog.Printf("Cannot detect MIME type of %s: %v. Skipping...\n", path, err)
			continue
		}

		mimeString := mime.String()
		fileExtension := filepath.Ext(path)
		if !slices.Contains(fileFormats, mimeString) {
			warnLog.Printf(
				"File format of %s (%s) is unsupported. Skipping...\n",
				path, mimeString,
			)

			continue
		}

		var usedPath string

		validExtensions := fileExtensions[mimeString]
		if !slices.Contains(validExtensions, fileExtension) {
			var ok bool
			usedPath, ok = tryRenameMismatchedFile(renameMode, path, validExtensions[0], mimeString, printLog, warnLog)
			if !ok {
				continue
			}
		} else {
			usedPath = path
		}

		collection, ok := pathCollection[mimeString]
		if !ok {
			collection = make([]string, 0)
		}

		collection = append(collection, usedPath)
		pathCollection[mimeString] = collection
	}

	operations = make([]*OperatedFiles, 0, len(pathCollection))
	for mimeString, mimePaths := range pathCollection {
		operations = append(operations, &OperatedFiles{
			Paths:     mimePaths,
			Extension: fileExtensions[mimeString][0],
			Mime:      mimeString,

			WarnLog:  warnLog,
			PrintLog: printLog,
		},
		)
	}

	return operations
}

func (of *OperatedFiles) WriteReport(process *compressor.CompressionProcess) (err error) {
	reportDir, _ := filepath.Split(of.Paths[0])
	compressReport, err := report.NewCompressReport(reportDir, of.Extension)
	if err != nil {
		of.WarnLog.Printf("Cannot create report for file format %s: %v\n", of.Extension, err)
		return err
	}

	err = compressReport.WriteProcess(process)
	if err != nil {
		of.WarnLog.Printf("Error occurred while writing report for file format %s: %v\n", of.Extension, err)
		return err
	}

	err = compressReport.FlushToFile(of.PrintLog)
	if err != nil {
		of.WarnLog.Printf("Error occurred while finalising report for file format %s: %v\n", of.Extension, err)
		return err
	}

	of.PrintLog.Printf("Result written to %s.\n\n", compressReport.Path)
	return nil
}

func (of *OperatedFiles) SetTools(cfg *config.Config, preset string, toolNames []string) {
	perFileTools := make(map[string]compressor.ExecutedTool)
	batchableTools := make(map[string]compressor.ExecutedTool)

	for _, toolName := range toolNames {
		tool, ok := cfg.Tools[toolName]
		if !ok {
			of.WarnLog.Printf("Attempting to run unknown tool %s. Skipping...", toolName)
			continue
		}

		if !cfg.IsToolAvailable(toolName) {
			continue
		}

		if !slices.Contains(tool.SupportedFormats, of.Mime) {
			continue
		}

		executedTool, ok := compressor.ToolConfigToExecutedTool(tool, preset)
		if !ok {
			continue
		}

		if tool.CanBatchCompress() {
			batchableTools[toolName] = executedTool
		} else {
			perFileTools[toolName] = executedTool
		}
	}

	of.BatchableTools = batchableTools
	of.PerFileTools = perFileTools
}

func (of *OperatedFiles) SetDefaultTools(cfg *config.Config, preset, mime string) {
	of.SetTools(cfg, preset, cfg.Presets[preset].DefaultTools[mime])
}

func (of *OperatedFiles) ForcePerFileMode() {
	maps.Copy(of.PerFileTools, of.BatchableTools)
	clear(of.BatchableTools)
}

func tryRenameMismatchedFile(
	renameMode RenameMode,
	path string,
	correctExtension string,
	mimeString string,
	printLog,
	warnLog *log.Logger,
) (corrected string, ok bool) {

	fileExtension := filepath.Ext(path)
	if renameMode == ForceDecline {
		warnLog.Printf(
			"File %s is actually a %s despite the extension being %s. Skipping...",
			path, mimeString, fileExtension,
		)
		return path, false
	}

	directory, fileName := filepath.Split(path)
	corrected = filepath.Join(directory, strings.TrimSuffix(fileName, fileExtension)+correctExtension)
	warnLog.Printf(
		"File %s is actually a %s despite the extension being %s. Trying to rename.",
		path, mimeString, filepath.Ext(path),
	)

	var isAccepted bool
	switch renameMode {
	case ForceAccept:
		isAccepted = renameMismatched(path, corrected, printLog, warnLog)
	case PromptUser:
		isAccepted = promptRenameMismatched(path, corrected, printLog, warnLog)
	}

	if !isAccepted {
		return path, false
	}

	return corrected, true
}

func renameMismatched(path, correctedPath string, printLog, warnLog *log.Logger) (ok bool) {
	err := os.Rename(path, correctedPath)
	if err != nil {
		warnLog.Printf("Cannot rename file %s: %v. File is skipped...\n", path, err)
		return false
	}

	printLog.Printf("File successfully renamed.")
	return true
}

func promptRenameMismatched(path, correctedPath string, printLog, warnLog *log.Logger) (ok bool) {
	fmt.Fprintf(os.Stderr, "Would you like to rename this file to %s? (y/n): ", filepath.Base(correctedPath))

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	input = strings.TrimSpace(strings.ToLower(input))

	if input == "y" || input == "yes" {
		return renameMismatched(path, correctedPath, printLog, warnLog)
	}

	fmt.Fprintf(os.Stderr, "Skipping %s.\n", path)
	return false
}
