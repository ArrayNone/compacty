package report

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ArrayNone/compacty/internal/compressor"
	"github.com/ArrayNone/compacty/internal/maputils"
)

type CompressReport struct {
	writer *csv.Writer
	file   *os.File

	Path string
}

func NewCompressReport(directory, extensionName string) (report *CompressReport, err error) {
	path := filepath.Join(directory, "result"+extensionName+".tsv")

	file, err := os.Create(path)
	if err != nil {
		return nil, err
	}

	csvWriter := csv.NewWriter(file)
	csvWriter.Comma = '\t'

	return &CompressReport{
		Path: path,

		writer: csvWriter,
		file:   file,
	}, nil
}

func (cr *CompressReport) WriteProcess(process *compressor.CompressionProcess) (err error) {
	header := []string{
		"File",
		"Tool",
		"Command",
		"Time (s)",
		"Final Size (MB)",
		"Reduction (MB)",
		"Reduction (%)",
	}

	if process.AreDecodeTimeComputed {
		header = append(
			header,
			fmt.Sprintf("Decode Time (ms avg within %v, w/ Go's native libraries)", process.MinDecodeTime),
			"Decode Trials",
		)
	}

	err = cr.writer.Write(header)
	if err != nil {
		return err
	}

	sortedToolNames := maputils.SortedKeys(process.Results)
	for i, fileInfo := range process.OriginalFileInfo {
		originalLine := []string{
			fileInfo.FileName,
			"original",
			"-",
			"-",
			strconv.FormatFloat(toMegaByte(fileInfo.Size), 'f', 6, 64),
			"0.000000",
			"100.000000%",
		}

		if process.AreDecodeTimeComputed {
			originalLine = append(
				originalLine,
				fileInfo.Decode.MSAverageToString(),
				strconv.Itoa(fileInfo.Decode.Trials),
			)
		}

		err = cr.writer.Write(originalLine)
		if err != nil {
			return err
		}

		for _, toolName := range sortedToolNames {
			result := process.Results[toolName][i]
			resultLine := buildResultLine(fileInfo.FileName, toolName, result)

			if process.AreDecodeTimeComputed {
				resultLine = expandResultLineWithDecodeTime(resultLine, result)
			}

			err = cr.writer.Write(resultLine)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (cr *CompressReport) FlushToFile() (err error) {
	cr.writer.Flush()
	err = cr.writer.Error()
	if err != nil {
		return err
	}

	err = cr.file.Close()
	if err != nil {
		return err
	}

	return nil
}

func commandWithArgsString(cmdArgs []string, toolArgCount int) string {
	return strings.Join(cmdArgs[0:toolArgCount+1], " ")
}

func buildResultLine(fileName, toolName string, result *compressor.CompressionResult) (fields []string) {
	commandWithArgs := commandWithArgsString(result.Command.Args, len(result.Arguments))

	if result.CreateError != nil {
		return []string{fileName, toolName, commandWithArgs, "CANNOT CREATE OUTPUT", "-", "-", "-", "-"}
	}

	if result.CommandError != nil {
		return []string{fileName, toolName, commandWithArgs, "COMMAND FAILED", "-", "-", "-", "-"}
	}

	if result.ReadFinalSizeError != nil {
		return []string{
			fileName,
			toolName,
			commandWithArgs,
			strconv.FormatFloat(result.TimeTaken.Seconds(), 'f', 6, 64),
			"CANNOT READ FILE SIZE",
			"-",
			"-",
		}
	}

	originalSize := result.OriginalSize
	finalSize := result.FinalSize
	percentage := (float64(finalSize) / float64(originalSize)) * 100

	return []string{
		fileName,
		toolName,
		commandWithArgs,
		strconv.FormatFloat(result.TimeTaken.Seconds(), 'f', 6, 64),
		strconv.FormatFloat(toMegaByte(finalSize), 'f', 6, 64),
		strconv.FormatFloat(toMegaByte(originalSize-finalSize), 'f', 6, 64),
		strconv.FormatFloat(percentage, 'f', 6, 64) + "%",
	}
}

func expandResultLineWithDecodeTime(fields []string, result *compressor.CompressionResult) []string {
	if result.CommandError != nil || result.CreateError != nil {
		fields = append(
			fields,
			"-",
			"-",
		)
	} else {
		fields = append(
			fields,
			result.Decode.MSAverageToString(),
			strconv.Itoa(result.Decode.Trials),
		)
	}

	return fields
}

func toMegaByte(sizeInByte int64) float64 {
	const bytePerMegabyte = 1000000
	return float64(sizeInByte) / bytePerMegabyte
}
