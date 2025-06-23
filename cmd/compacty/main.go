package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"maps"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ArrayNone/compacty/internal/compressor"
	"github.com/ArrayNone/compacty/internal/config"
	"github.com/ArrayNone/compacty/internal/maputils"
	"github.com/ArrayNone/compacty/internal/textutils"

	"github.com/fatih/color"
	"github.com/spf13/pflag"
)

type CLIArguments struct {
	Preset        string
	ConfigPath    string
	SelectedTools []string
	DecodeMeasure time.Duration

	All       bool
	Quiet     bool
	ToolPrint bool

	Overwrite bool
	KeepAll   bool
	Dry       bool

	ActionVersion       bool
	ActionHelp          bool
	ActionList          bool
	ActionResetConfig   bool
	ActionGetConfigPath bool

	PerFile        bool
	Report         bool
	ForceRename    bool
	NoRename       bool
	SkipValidation bool
	DecodeTime     bool
	NoColour       bool
}

const defaultDecodeMeasure = time.Millisecond * 500

var version = "dev"

func main() {
	args := parseArgs()
	if args.NoColour {
		color.NoColor = true
	}

	if err := run(args); err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", color.RedString("Error:"), err.Error())

		var exitErr *ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		} else {
			os.Exit(1)
		}
	}
}

func run(cliArguments *CLIArguments) (err error) {
	printLog := log.New(os.Stdout, "", 0)
	warnLog := log.New(os.Stderr, color.YellowString("Warning: "), 0)
	if cliArguments.ForceRename && cliArguments.NoRename {
		return &ExitCodeError{
			Err:  errors.New("cannot pass both --force-rename and --no-rename at once"),
			Code: BadUsage,
		}
	}

	if cliArguments.ActionVersion {
		printLog.Println("compacty", version)
		return nil
	}

	if cliArguments.ActionHelp {
		printHelp()
		return nil
	}

	if cliArguments.Quiet {
		printLog.SetOutput(io.Discard)
		warnLog.SetOutput(io.Discard)
	}

	if cliArguments.ConfigPath == "" {
		defaultConfigPath, isCreated, err := config.GetOrCreateUserConfigFile(warnLog)
		if err != nil {
			return &ExitCodeError{
				Err:  fmt.Errorf("can't retrieve config file: %w", err),
				Code: CannotRetrieveConfig,
			}
		}

		if isCreated && !cliArguments.ActionGetConfigPath {
			printLog.Println("User config file does not exist, created a default on:", defaultConfigPath)
		}

		cliArguments.ConfigPath = defaultConfigPath
	}

	if cliArguments.ActionGetConfigPath {
		printLog.Println(cliArguments.ConfigPath)
		return nil
	}

	if cliArguments.ActionResetConfig {
		err := config.CreateDefaultConfig(cliArguments.ConfigPath)
		if err != nil {
			return &ExitCodeError{
				Err:  fmt.Errorf("cannot reset config file at %s: %w", cliArguments.ConfigPath, err),
				Code: CannotRetrieveConfig,
			}
		}

		printLog.Printf("Config file at %s has been reset.\n", cliArguments.ConfigPath)

		// Exit early, do not print help message
		if pflag.NArg() == 0 {
			return nil
		}
	}

	loadedConfig, err := config.DecodeConfigFile(cliArguments.ConfigPath)
	if err != nil {
		return &ExitCodeError{
			Err:  fmt.Errorf("cannot read config file at %s: %w", cliArguments.ConfigPath, err),
			Code: BadConfig,
		}
	}

	if !cliArguments.SkipValidation {
		configErrors := loadedConfig.Validate()

		if len(configErrors) > 0 {
			return &ExitCodeError{
				Err: errors.Join(
					fmt.Errorf("config file at %s is invalid", cliArguments.ConfigPath),
					errors.Join(configErrors...),
				),
				Code: BadConfig,
			}
		}
	}

	if cliArguments.ActionList {
		list(loadedConfig, cliArguments.ConfigPath, printLog.Writer())
		return nil
	}

	if pflag.NArg() == 0 {
		printHelp()

		return &ExitCodeError{
			Err:  errors.New("no files provided"),
			Code: BadUsage,
		}
	}

	var usedPreset, queriedPreset string
	if cliArguments.Preset == "" {
		queriedPreset = loadedConfig.DefaultPreset
	} else {
		queriedPreset = cliArguments.Preset
	}

	usedPreset, isShorthand := config.QueryPreset(loadedConfig.Presets, queriedPreset)
	if usedPreset == "" {
		var builder strings.Builder
		writePresets(&builder, loadedConfig)

		// Show preset on error to the output ONLY
		fmt.Fprint(os.Stderr, builder.String())

		return &ExitCodeError{
			Err:  fmt.Errorf("attempting to use unknown preset: %s", queriedPreset),
			Code: BadUsage,
		}
	}

	if cliArguments.DecodeTime {
		warnLog.Println("Decode time benchmarking is EXPERIMENTAL and MAY NOT reflect real-world performance!")
	}

	usingPresetStr := color.BlueString("Using preset:")
	if isShorthand {
		printLog.Println(usingPresetStr, queriedPreset, color.CyanString("->"), usedPreset)
	} else {
		printLog.Println(usingPresetStr, usedPreset)
	}

	if cliArguments.All {
		cliArguments.SelectedTools = slices.Collect(maps.Keys(loadedConfig.Tools))
	}

	paths := pflag.Args()

	renameMode := cliArguments.RenameMode()
	operatedFiles := PathsToOperatedFiles(loadedConfig, paths, renameMode, printLog, warnLog)
	for _, operation := range operatedFiles {
		if !pflag.Lookup("tools").Changed && !cliArguments.All {
			operation.SetDefaultTools(loadedConfig, usedPreset, operation.Mime)
		} else {
			operation.SetTools(loadedConfig, usedPreset, cliArguments.SelectedTools)
		}

		if cliArguments.PerFile {
			operation.ForcePerFileMode()
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	toolOutput := cliArguments.ToolOutput()
	writeMode := cliArguments.WriteMode()
	wrappers := loadedConfig.Wrappers[runtime.GOOS]

	var hasTools, isRan, hasErrors bool

	markErrorIfNotOk := func(ok bool) {
		if !ok {
			hasErrors = true
		}
	}

	for _, operation := range operatedFiles {
		if len(operation.BatchableTools) == 0 && len(operation.PerFileTools) == 0 {
			warnLog.Printf("No valid tools found for file format %s (%s).\n", operation.Extension, operation.Mime)
			continue
		}

		hasTools = true

		process, allOk := compressor.NewCompressionProcess(operation.Paths, wrappers, printLog, warnLog, toolOutput)
		defer process.CleanUp()
		markErrorIfNotOk(allOk)

		validCount := len(process.OriginalPaths)
		if validCount == 0 {
			warnLog.Printf("Cannot find valid %s paths.\n", operation.Extension)
			continue
		}

		isRan = true

		if len(operation.BatchableTools) > 0 {
			fileText := textutils.PluralNoun(validCount, "files", "file")
			printLog.Println(
				color.BlueString("Compressing %d %s %s:", validCount, operation.Extension, fileText),
				strings.Join(process.OriginalPaths, " "),
			)

			select {
			case <-process.CompressAll(ctx, operation.BatchableTools):
			case <-ctx.Done():
				return &ExitCodeError{Err: errors.New("interrupted"), Code: Interrupted}
			}

			printLog.Println()
		}

		if len(operation.PerFileTools) > 0 {
			if !cliArguments.PerFile || len(operation.PerFileTools) == 1 {
				printLog.Println("Running per-file tools.")
			}

			for i, path := range process.OriginalPaths {
				printLog.Println(
					color.BlueString("Compressing %s file #%d:", operation.Extension, i+1),
					color.CyanString(path),
				)

				select {
				case <-process.CompressSingle(i, ctx, operation.PerFileTools):
				case <-ctx.Done():
					return &ExitCodeError{Err: errors.New("interrupted"), Code: Interrupted}
				}

				printLog.Println()
			}
		}

		if cliArguments.DecodeTime {
			select {
			case <-process.BenchmarkDecodeTime(cliArguments.DecodeMeasure):
			case <-ctx.Done():
				return &ExitCodeError{Err: errors.New("interrupted"), Code: Interrupted}
			}
		}

		markErrorIfNotOk(process.SaveResultsAndReport(writeMode))
		markErrorIfNotOk(process.IsErrorFree())

		if cliArguments.Report {
			ok := operation.WriteReport(process) == nil
			markErrorIfNotOk(ok)
		}
	}

	if !hasTools {
		if !loadedConfig.HasAvailableTools() {
			list(loadedConfig, cliArguments.ConfigPath, printLog.Writer())
			printLog.Print(
				"Note that compacty runs other compression tools. As such, these need to be either installed\n",
				"in your PATH or the tool's executable must be placed in the same directory as compacty.\n",
				"Scroll the list above to see your available presets and compression tools.\n",
			)

		}

		return &ExitCodeError{
			Err:  errors.New("no valid tools"),
			Code: BadUsage,
		}
	} else if !isRan {
		return &ExitCodeError{
			Err:  errors.New("no valid inputs"),
			Code: BadInput,
		}
	} else if hasErrors {
		return errors.New("error(s) occurred during compression")
	}

	return nil
}

func parseArgs() (args *CLIArguments) {
	args = &CLIArguments{}

	pflag.StringVarP(&args.Preset, "preset", "p", "", "Select preset (run tool with --list to see all available presets)")
	pflag.StringVarP(&args.ConfigPath, "config", "c", "", "Use a config file from this path instead from your config directory")
	pflag.StringSliceVarP(&args.SelectedTools, "tools", "t", []string{}, "Select available tools. Separated by commas (example: --tool=ect,pingo)")
	pflag.DurationVar(&args.DecodeMeasure, "dt-measure", defaultDecodeMeasure, "Measure decode time for at least the specified duration per file and their compression results in combination with --decode-time")

	pflag.BoolVarP(&args.All, "all", "a", false, "Use all available tools. Flag is ignored when --tools are provided")
	pflag.BoolVarP(&args.Quiet, "quiet", "q", false, "Suppress outputs")
	pflag.BoolVar(&args.ToolPrint, "tool-print", false, "Print tool outputs, ignores --quiet")
	pflag.BoolVar(&args.NoColour, "no-color", false, "Disable coloured output")

	pflag.BoolVarP(&args.ActionVersion, "version", "v", false, "Print version and exit")
	pflag.BoolVarP(&args.ActionHelp, "help", "h", false, "Print usage help and exit")
	pflag.BoolVarP(&args.ActionList, "list", "l", false, "Print tools and presets from the loaded config file and exit")
	pflag.BoolVar(&args.ActionResetConfig, "reset-config", false, " Resets the config file at the user's config directory to default. If --config is provided, creates/resets the file at path instead")
	pflag.BoolVar(&args.ActionGetConfigPath, "get-config-path", false, "Print the config path and exit")

	pflag.BoolVarP(&args.Overwrite, "overwrite", "O", false, "Overwrite input files")
	pflag.BoolVar(&args.KeepAll, "keep-all", false, "Keep all compressed files, including losing ones")
	pflag.BoolVar(&args.Dry, "dry", false, "Compress and show results only; keep files intact")

	pflag.BoolVar(&args.Report, "report", false, "Save compression results in .tsv files")
	pflag.BoolVar(&args.PerFile, "per-file", false, "Force files to be compressed one by one, intended for per-file benchmarking")
	pflag.BoolVar(&args.ForceRename, "force-rename", false, "Automatically rename files with mislabeled extensions when prompted")
	pflag.BoolVar(&args.NoRename, "no-rename", false, "Skip renaming files with mislabeled extensions automatically when prompted")
	pflag.BoolVar(&args.DecodeTime, "decode-time", false, "[EXPERIMENTAL] Measure decode time using Go's native libraries (PNG and JPEG only)")
	pflag.BoolVar(&args.SkipValidation, "skip-validation", false, "[UNSUPPORTED] Skip config validation. May cause runtime errors and/or crash. USE AT YOUR OWN RISK!")

	pflag.Usage = printHelp
	pflag.Parse()

	return args
}

func (cli *CLIArguments) WriteMode() compressor.WriteMode {
	if cli.Dry {
		return compressor.None
	} else if cli.Overwrite {
		return compressor.Overwrite
	} else if cli.KeepAll {
		return compressor.KeepAll
	} else {
		return compressor.KeepBest
	}
}

func (cli *CLIArguments) RenameMode() RenameMode {
	if cli.ForceRename {
		return ForceAccept
	} else if cli.NoRename {
		return ForceDecline
	} else {
		return PromptUser
	}
}

func (cli *CLIArguments) ToolOutput() io.Writer {
	if cli.ToolPrint {
		return os.Stdout
	} else {
		return io.Discard
	}
}

func printHelp() {
	blue := color.New(color.FgBlue).SprintFunc()

	fmt.Fprintf(os.Stderr, `Compress files by using multiple compression tools and pick the best result.
%s compacty [OPTIONS] <files>...

%s
  -p, --preset=<NAME>   Select preset (run tool with --list to see all available presets)
  -c, --config=<PATH>   Use a config file from this path instead from your config directory
  -t, --tools=<TOOL,..> Select available tools. Separated by commas (example: --tool=ect,pingo)
  -a, --all             Use all available tools. Flag is ignored when --tools are provided
  -q, --quiet           Suppress outputs
      --tool-print      Print tool outputs, ignores --quiet
      --no-color        Disable coloured output

  -v, --version         Print version and exit
  -h, --help            Print usage help and exit
  -l, --list            Print tools and presets from the loaded config file and exit
      --reset-config    Resets the config file at the user's config directory to default. If --config is provided, creates/resets the file at path instead
      --get-config-path Print the config path and exit

%s
  -O, --overwrite       Overwrite input files
      --keep-all        Keep all compressed files, including losing ones
      --dry             Compress and show results only; keep files intact

%s
      --report          Save compression results in .tsv files
      --per-file        Force files to be compressed one by one, intended for per-file benchmarking
      --force-rename    Automatically rename files with mislabeled extensions when prompted
      --no-rename       Skip renaming files with mislabeled extensions automatically when prompted

      --decode-time     [EXPERIMENTAL] Measure decode time using Go's native libraries (PNG, JPEG, and GIF only)
      --dt-measure=<t>  Measure decode time for at least the specified duration per file and their compression results in combination with --decode-time

      --skip-validation [UNSUPPORTED] Skip config validation. May cause runtime errors and/or crash. USE AT YOUR OWN RISK!
`, blue("Usage:"), blue("Options:"), blue("Save modes:"), blue("Advanced options:"))
}

func list(cfg *config.Config, configPath string, output io.Writer) {
	var builder strings.Builder

	builder.WriteString("Config loaded from ")
	builder.WriteString(configPath)
	builder.WriteString("\n\n")

	writeTools(&builder, cfg)
	writePresets(&builder, cfg)

	fmt.Fprint(output, builder.String())
}

func writeTools(builder *strings.Builder, cfg *config.Config) {
	builder.WriteString(color.BlueString("Tools:"))
	builder.WriteByte('\n')

	sortedToolNames := maputils.SortedKeys(cfg.Tools)

	for _, toolName := range sortedToolNames {
		tool := cfg.Tools[toolName]

		builder.WriteString(toolName)
		wrapper := cfg.QueryToolWrapper(tool, runtime.GOOS)
		if wrapper != "" {
			builder.WriteByte(' ')
			fmt.Fprintf(builder, "(wrapped, requires %s)", wrapper)
		}

		if cfg.IsToolAvailable(toolName) {
			builder.WriteByte(' ')
			builder.WriteString(color.CyanString("(available)"))
		}

		builder.WriteByte('\n')

		fmt.Fprintf(builder, "| Description:\n|   ")
		builder.WriteString(tool.Description)
		builder.WriteByte('\n')

		fmt.Fprintf(builder, "| Supported file formats:\n|   ")
		builder.WriteString(strings.Join(tool.SupportedFormats, ", "))
		builder.WriteString("\n\n")
	}
}

func writePresets(builder *strings.Builder, cfg *config.Config) {
	builder.WriteString(color.BlueString("Presets:"))
	builder.WriteByte('\n')

	sortedPresetNames := maputils.SortedKeys(cfg.Presets)

	for _, presetName := range sortedPresetNames {
		preset := cfg.Presets[presetName]

		builder.WriteString(presetName)
		shorthands := cfg.Presets[presetName].Shorthands
		if len(shorthands) > 0 {
			builder.WriteString(" ")
			builder.WriteString(color.CyanString("="))
			builder.WriteString(" ")
			builder.WriteString(strings.Join(shorthands, ", "))
		}

		if cfg.DefaultPreset == presetName {
			builder.WriteByte(' ')
			builder.WriteString(color.CyanString("(default)"))
		}

		builder.WriteByte('\n')

		builder.WriteString("| Description:\n|   ")
		builder.WriteString(preset.Description)
		builder.WriteString("\n")

		builder.WriteString("| Tools ran by default:\n")

		sortedDefaultTools := maputils.SortedKeys(preset.DefaultTools)
		for _, format := range sortedDefaultTools {
			defaultToolNames := preset.DefaultTools[format]

			if len(defaultToolNames) == 0 {
				continue
			}

			builder.WriteString("|   ")
			builder.WriteString(format)
			builder.WriteString(": ")

			for i, toolName := range defaultToolNames {
				if cfg.IsToolAvailable(toolName) {
					builder.WriteString(color.CyanString(toolName))
				} else {
					builder.WriteString(toolName)
				}

				isLast := i+1 == len(defaultToolNames)
				if !isLast {
					builder.WriteString(", ")
				}
			}

			builder.WriteByte('\n')
		}

		builder.WriteByte('\n')
	}
}
