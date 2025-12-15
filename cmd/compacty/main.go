package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	"github.com/ArrayNone/compacty/internal/prints"
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
	ActionListArgs      string
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
		fmt.Fprintln(os.Stderr, color.RedString("Error:"), err.Error())

		var exitErr *ExitCodeError
		if errors.As(err, &exitErr) {
			os.Exit(exitErr.Code)
		} else {
			os.Exit(1)
		}
	}
}

func run(cliArguments *CLIArguments) (err error) {
	if cliArguments.ForceRename && cliArguments.NoRename {
		return &ExitCodeError{
			Err:  errors.New("cannot pass both --force-rename and --no-rename at once"),
			Code: BadUsage,
		}
	}

	if cliArguments.ActionVersion {
		prints.Println("compacty", version)
		return nil
	}

	if cliArguments.ActionHelp {
		printHelp()
		return nil
	}

	if cliArguments.Quiet {
		prints.IsQuiet = true
	}

	if cliArguments.ConfigPath == "" {
		defaultConfigPath, isCreated, err := config.GetOrCreateUserConfigFile()
		if err != nil {
			return &ExitCodeError{
				Err:  fmt.Errorf("can't retrieve config file: %w", err),
				Code: CannotRetrieveConfig,
			}
		}

		if isCreated && !cliArguments.ActionGetConfigPath {
			prints.Println("User config file does not exist, created a default on:", defaultConfigPath)
		}

		cliArguments.ConfigPath = defaultConfigPath
	}

	if cliArguments.ActionGetConfigPath {
		prints.Println(cliArguments.ConfigPath)
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

		prints.Printf("Config file at %s has been reset.\n", cliArguments.ConfigPath)

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
		list(loadedConfig, cliArguments.ConfigPath)
		return nil
	}

	if pflag.Lookup("list-args").Changed {
		listArgs(loadedConfig, cliArguments.ActionListArgs, cliArguments.ConfigPath)
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
		prints.Warnln("Decode time benchmarking is EXPERIMENTAL and MAY NOT reflect real-world performance!")
	}

	usingPresetStr := color.BlueString("Using preset:")
	if isShorthand {
		prints.Println(usingPresetStr, queriedPreset, color.CyanString("->"), usedPreset)
	} else {
		prints.Println(usingPresetStr, usedPreset)
	}

	if cliArguments.All {
		cliArguments.SelectedTools = slices.Collect(maps.Keys(loadedConfig.Tools))
	}

	paths := pflag.Args()

	renameMode := cliArguments.RenameMode()
	operatedFiles := PathsToOperatedFiles(loadedConfig, paths, renameMode)
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

	toolOutput := cliArguments.ToolOutput()
	writeMode := cliArguments.WriteMode()
	wrappers := loadedConfig.Wrappers[runtime.GOOS]

	var hasTools, isRan, hasErrors bool

	markErrorIfNotOk := func(ok bool) {
		if !ok {
			hasErrors = true
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for _, operation := range operatedFiles {
		if len(operation.BatchableTools) == 0 && len(operation.PerFileTools) == 0 {
			prints.Warnf("No valid tools found for file format %s (%s).\n", operation.Extension, operation.Mime)
			continue
		}

		hasTools = true

		process, allOk := compressor.NewCompressionProcess(operation.Paths, wrappers, toolOutput)
		defer process.CleanUp()
		markErrorIfNotOk(allOk)

		validCount := len(process.OriginalPaths)
		if validCount == 0 {
			prints.Warnf("Cannot find valid %s paths.\n", operation.Extension)
			continue
		}

		isRan = true

		if len(operation.BatchableTools) > 0 {
			fileText := textutils.PluralNoun(validCount, "files", "file")
			prints.Println(
				color.BlueString("Compressing %d %s %s:", validCount, operation.Extension, fileText),
				strings.Join(process.OriginalPaths, " "),
			)

			select {
			case <-process.CompressAll(ctx, operation.BatchableTools):
			case <-ctx.Done():
				return &ExitCodeError{Err: errors.New("interrupted"), Code: Interrupted}
			}

			prints.Println()
		}

		if len(operation.PerFileTools) > 0 {
			if !cliArguments.PerFile || len(operation.PerFileTools) == 1 {
				prints.Println("Running per-file tools.")
			}

			for i, path := range process.OriginalPaths {
				prints.Println(
					color.BlueString("Compressing %s file #%d:", operation.Extension, i+1),
					color.CyanString(path),
				)

				select {
				case <-process.CompressSingle(i, ctx, operation.PerFileTools):
				case <-ctx.Done():
					return &ExitCodeError{Err: errors.New("interrupted"), Code: Interrupted}
				}

				prints.Println()
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
			list(loadedConfig, cliArguments.ConfigPath)
			prints.Print(
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
	pflag.BoolVar(&args.NoColour, "no-colour", false, "Disable coloured output (alt)")

	pflag.BoolVarP(&args.ActionVersion, "version", "v", false, "Print version and exit")
	pflag.BoolVarP(&args.ActionHelp, "help", "h", false, "Print usage help and exit")
	pflag.BoolVarP(&args.ActionList, "list", "l", false, "Print tools and presets from the loaded config file and exit")
	pflag.StringVar(&args.ActionListArgs, "list-args", "", "Print tool arguments from the loaded config file and exit. Use --list-args=raw to list arguments while keeping includes intact")
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
  -p, --preset=NAME     Select preset (run tool with --list to see all available presets)
  -c, --config=PATH     Use a config file from this path instead from your config directory
  -t, --tools=TOOL,...  Select available tools. Separated by commas (example: --tool=ect,pingo)
  -a, --all             Use all available tools. Flag is ignored when --tools are provided
  -q, --quiet           Suppress outputs
      --tool-print      Print tool outputs, ignores --quiet
      --no-colo[u]r     Disable coloured output

  -v, --version         Print version and exit
  -h, --help            Print usage help and exit
  -l, --list            Print tools and presets from the loaded config file and exit
      --list-args=MODE  Print tool arguments from the loaded config file and exit. Modes: "raw" (keep includes intact), "dump" (resolve includes)

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
      --dt-measure=TIME Measure decode time for at least the specified duration per file and their compression results in combination with --decode-time

      --skip-validation [UNSUPPORTED] Skip config validation. May cause runtime errors and/or crash. USE AT YOUR OWN RISK!

`, blue("Usage:"), blue("Options:"), blue("Save modes:"), blue("Advanced options:"))
}

func listArgs(cfg *config.Config, mode, configPath string) {
	var builder strings.Builder

	builder.WriteString("Config loaded from ")
	builder.WriteString(configPath)
	builder.WriteString("\n\n")

	sortedToolNames := maputils.SortedKeys(cfg.Tools)
	for _, toolName := range sortedToolNames {
		tool := cfg.Tools[toolName]

		if cfg.IsToolAvailable(toolName) {
			builder.WriteString(color.CyanString(toolName))
		} else {
			builder.WriteString(toolName)
		}

		builder.WriteByte('\n')

		sortedPresetNames := maputils.SortedKeys(tool.Arguments)
		for _, presetName := range sortedPresetNames {
			args := tool.Arguments[presetName]
			if mode != "raw" {
				var errs []error
				args, errs = tool.ResolveIncludesForPreset(presetName, toolName)

				if len(errs) > 0 {
					builder.WriteString(presetName)
					builder.WriteByte(' ')
					builder.WriteString(color.RedString("ERROR: "))
					builder.WriteString(errors.Join(errs...).Error())

					continue
				}
			}

			builder.WriteString("| ")
			builder.WriteString(presetName)
			builder.WriteString(": ")
			builder.WriteString(strings.Join(args, " "))

			builder.WriteByte('\n')
		}

		builder.WriteByte('\n')
	}

	fmt.Print(builder.String())
}

func list(cfg *config.Config, configPath string) {
	var builder strings.Builder

	builder.WriteString("Config loaded from ")
	builder.WriteString(configPath)
	builder.WriteString("\n\n")

	writeTools(&builder, cfg)
	writePresets(&builder, cfg)

	fmt.Print(builder.String())
}

func writeTools(builder *strings.Builder, cfg *config.Config) {
	builder.WriteString(color.BlueString("Tools:\n"))

	sortedToolNames := maputils.SortedKeys(cfg.Tools)
	for _, toolName := range sortedToolNames {
		tool := cfg.Tools[toolName]

		builder.WriteString(toolName)
		wrapper := cfg.QueryToolWrapper(tool, runtime.GOOS)
		if wrapper != "" {
			builder.WriteString(" (wrapped, requires ")
			builder.WriteString(wrapper)
			builder.WriteByte(')')
		}

		if cfg.IsToolAvailable(toolName) {
			builder.WriteByte(' ')
			builder.WriteString(color.CyanString("(available)"))
		}

		builder.WriteByte('\n')

		builder.WriteString("| Description:\n|   ")
		builder.WriteString(tool.Description)
		builder.WriteByte('\n')

		builder.WriteString("| Supported file formats:\n|   ")
		builder.WriteString(strings.Join(tool.SupportedFormats, ", "))
		builder.WriteString("\n\n")
	}
}

func writePresets(builder *strings.Builder, cfg *config.Config) {
	builder.WriteString(color.BlueString("Presets:\n"))

	sortedPresetNames := maputils.SortedKeys(cfg.Presets)
	for _, presetName := range sortedPresetNames {
		preset := cfg.Presets[presetName]

		builder.WriteString(presetName)
		shorthands := cfg.Presets[presetName].Shorthands
		if len(shorthands) > 0 {
			builder.WriteString(" [aka: ")
			builder.WriteString(strings.Join(shorthands, ", "))
			builder.WriteString("]")
		}

		if cfg.DefaultPreset == presetName {
			builder.WriteByte(' ')
			builder.WriteString(color.CyanString("(default)"))
		}

		builder.WriteByte('\n')

		builder.WriteString("| Description:\n|   ")
		builder.WriteString(preset.Description)
		builder.WriteByte('\n')

		builder.WriteString("| Tools ran by default:\n")

		sortedDefaultTools := maputils.SortedKeys(preset.DefaultTools)
		for _, format := range sortedDefaultTools {
			defaultToolNames := preset.DefaultTools[format]

			if len(defaultToolNames) == 0 {
				continue
			}

			builder.WriteString("| - ")
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
