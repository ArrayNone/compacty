package config

import (
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/gabriel-vasile/mimetype"
	"go.yaml.in/yaml/v3"
)

/* YAML Schema:
default-preset: <preset name> # Default preset to run when --preset is not provided

mime-extensions:
  <MIME type> = [<extensions>] # Valid extensions for files with this mime type

wrappers:
  <platform name on os>: # Wrappers to run while running on the platform/OS
    <tool's supported platform name>: <wrapper> # Wrapper to run for tools that aren't native to the above platform/OS

presets: # Define presets, preset arguments for tools with a collection of default tools to run
  <preset name>:
    description: <description> # Preset description, what it does and what it's intended for
    shorthands: [<name>] # Alternative names for the preset
    default-tools:
      <MIME type>: [<tool names>] # Default tools to use for files with a certain MIME type

tools: # Define compression tools
  <tool name>:
    description: <description> # Tool description, typically describing what it does and a link to the homepage
    command: <name> # Tool/binary to be executed
    platform: [<platforms>] # Platforms/OSes where this tool can run ("windows", "linux", "darwin")
    supported-formats: [<MIME type>] # File formats the tool supports (in MIME format, eg. `image/png`, `text/plain`)
    overwrites: <bool> # If `true` the tool overwrites files that its given (some tools create a copy of the file instead)
    can-batch-compress: <bool> # If `true`, the tool supports compressing multiple files at once
    arguments:
      <preset name> = <string> # Arguments when running the tool with a specific preset, separated by spaces

*/

type CompressionTool struct {
	Command          string     `yaml:"command"`
	SupportedFormats []string   `yaml:"supported-formats"`
	Platform         []string   `yaml:"platform"`
	OutputMode       OutputMode `yaml:"output-mode"`
}

type ToolConfig struct {
	CompressionTool `yaml:",inline"`
	Description     string              `yaml:"description"`
	Arguments       map[string][]string `yaml:"arguments"`
}

type Preset struct {
	Description string   `yaml:"description"`
	Shorthands  []string `yaml:"shorthands"`

	DefaultTools map[string][]string `yaml:"default-tools"`
}

type Config struct {
	DefaultPreset string `yaml:"default-preset"`

	MimeExtensions map[string][]string `yaml:"mime-extensions"`

	Wrappers map[string]map[string]string `yaml:"wrappers"`
	Presets  map[string]Preset            `yaml:"presets"`
	Tools    map[string]*ToolConfig       `yaml:"tools"`

	isCached bool `yaml:"-"`

	supportedFileFormats    []string            `yaml:"-"`
	supportedFileExtensions map[string][]string `yaml:"-"`
	toolAvailability        map[string]struct{} `yaml:"-"`
}

type OutputMode int

const (
	Unknown OutputMode = iota
	BatchOverwrite
	InputOutput
	Stdout
)

func CreateDefaultConfig(path string) (err error) {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = file.WriteString(GetDefaultConfigStr())
	if err != nil {
		return err
	}

	return nil
}

func GetOrCreateUserConfigFile(warnLog *log.Logger) (path string, created bool, err error) {
	path, err = createUserConfigFilePath(warnLog)
	if err != nil {
		return "", false, fmt.Errorf("cannot retrieve the user's config path: %w", err)
	}

	if !fileExists(path) {
		err := CreateDefaultConfig(path)
		if err != nil {
			return "", false, fmt.Errorf("cannot create default config path at %s: %w", path, err)
		}

		created = true
	}

	return path, created, nil
}

func FindExecutablePath(executableName string, toolPlatform []string) (path string, ok bool) {
	path, err := exec.LookPath(executableName)
	if err == nil {
		return path, true
	}

	// Check current working directory
	// Go disallows running executables in the working directory without "./" or ".\", so we add that ourselves
	var relative string
	if runtime.GOOS == "windows" {
		relative = ".\\" + executableName
	} else {
		relative = "./" + executableName
	}

	if fileExists(relative) {
		return relative, true
	}

	if slices.Contains(toolPlatform, "windows") && !strings.HasSuffix(relative, ".exe") {
		exeRelative := relative + ".exe"
		if fileExists(exeRelative) {
			return exeRelative, true
		}
	}

	return "", false
}

func QueryWrapper(wrappers map[string]string, toolPlatform []string, currentPlatform string) (wrapper string) {
	if slices.Contains(toolPlatform, currentPlatform) {
		return ""
	}

	for _, platform := range toolPlatform {
		wrapper = wrappers[platform]
		if wrapper != "" {
			return wrapper
		}
	}

	return ""
}

func QueryPreset(presets map[string]Preset, name string) (preset string, isShorthand bool) {
	mappings := make(map[string]string)
	for presetName, presetData := range presets {
		mappings[presetName] = presetName
		for _, shorthand := range presetData.Shorthands {
			mappings[shorthand] = presetName
		}
	}

	if _, ok := mappings[name]; !ok {
		return "", false
	}

	isShorthand = mappings[name] != name
	return mappings[name], isShorthand
}

func DecodeConfigFile(path string) (cfg *Config, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var result Config
	err = yaml.Unmarshal(data, &result)
	result.Cache()
	return &result, err
}

func (cfg *Config) QueryToolWrapper(tool *ToolConfig, platform string) string {
	return QueryWrapper(cfg.Wrappers[runtime.GOOS], tool.Platform, runtime.GOOS)
}

func (cfg *Config) IsToolAvailable(toolName string) bool {
	_, ok := cfg.toolAvailability[toolName]
	return ok
}

func (cfg *Config) HasAvailableTools() bool {
	return len(cfg.toolAvailability) > 0
}

func (cfg *Config) GetSupportedFileFormats() (fileFormatsMime []string) {
	if !cfg.isCached {
		cfg.Cache()
	}

	return cfg.supportedFileFormats
}

func (cfg *Config) GetSupportedFileExtensions() (fileExtMap map[string][]string) {
	if !cfg.isCached {
		cfg.Cache()
	}

	return cfg.supportedFileExtensions
}

func (cfg *Config) GetToolConfigFromNames(toolNames []string) (toolCfgMap map[string]*ToolConfig) {
	result := make(map[string]*ToolConfig)
	for _, toolName := range toolNames {
		result[toolName] = cfg.Tools[toolName]
	}

	return result
}

func (cfg *Config) Validate() []error {
	if !cfg.isCached {
		cfg.Cache()
	}

	var Platforms = []string{"darwin", "dragonfly", "freebsd", "illumos", "linux", "netbsd", "openbsd", "plan9", "solaris", "windows"}

	const (
		undefinedDefaultPreset = "default-preset is not defined"
		unknownDefaultPreset   = "default-preset is an undefined preset: %s"

		mimeExtUnknownFormat   = "mime-extensions: %q is an unknown file format"
		mimeExtEmptyExtensions = "mime-extensions: %q has no defined file extensions"

		wrapperUnknownPlatform   = "wrapper: unknown platform defined: %s"
		wrapperUnknownPlatformIn = "wrapper: unknown platform defined in %q: %s"
		wrapperBlankCommand      = "wrapper: blank command defined in %q, then %q"

		presetShorthandConflict      = "preset: conflicting shorthand %q on multiple presets: %s"
		presetShorthandBlank         = "preset: shorthand on %q cannot be a blank name"
		presetUnknownDefaultFormat   = "preset: %q has unknown file format defined on default-tools: %s"
		presetUnknownDefaultTool     = "preset: %q included an undefined tool on default-tools at %q: %s"
		presetDefaultToolWithNoArgs  = "preset: %q included tool %q on default-tools with undefined arguments for this preset"
		presetDefaultToolUnsupported = "preset: %q included tool %q on default-tools for %s, which does not support this file format"

		toolUndefinedCommand    = "tool: %q has no command defined"
		toolUndefinedPlatform   = "tool: %q has no platforms defined"
		toolUnknownPlatform     = "tool: %q has unknown platform defined: %s"
		toolUndefinedFormat     = "tool: %q has no supported-formats defined"
		toolUnknownFileFormat   = "tool: %q has unknown file format defined: %s"
		toolUndefinedOutputMode = "tool: %q has no output-mode defined"
		toolUnknownOutputMode   = "tool: %q has unknown output-mode defined "
		toolUndefinedPresets    = "tool: %q has no arguments defined"
		toolUnknownPreset       = "tool: %q has unknown preset defined in arguments: %s"
	)

	var configErrors []error
	addErrorString := func(str string) {
		configErrors = append(configErrors, errors.New(str))
	}

	// default-preset
	if cfg.DefaultPreset == "" {
		addErrorString(undefinedDefaultPreset)
	} else {
		preset, _ := QueryPreset(cfg.Presets, cfg.DefaultPreset)
		if preset == "" {
			addErrorString(fmt.Sprintf(unknownDefaultPreset, cfg.DefaultPreset))
		}
	}

	// mime-extensions
	for format, extensions := range cfg.MimeExtensions {
		if mimetype.Lookup(format) == nil {
			addErrorString(fmt.Sprintf(mimeExtUnknownFormat, format))
		}

		if len(extensions) == 0 {
			addErrorString(fmt.Sprintf(mimeExtEmptyExtensions, format))
		}
	}

	// wrappers
	for wrapperOnPlatform, wrappers := range cfg.Wrappers {
		if !slices.Contains(Platforms, wrapperOnPlatform) {
			addErrorString(fmt.Sprintf(wrapperUnknownPlatform, wrapperOnPlatform))
		}

		for platform, wrapper := range wrappers {
			if wrapper == "" {
				addErrorString(fmt.Sprintf(wrapperBlankCommand, platform, wrapperOnPlatform))
			}

			if !slices.Contains(Platforms, platform) {
				addErrorString(fmt.Sprintf(wrapperUnknownPlatformIn, wrapperOnPlatform, platform))
			}
		}
	}

	// presets
	definedPresetNames := make([]string, 0, len(cfg.Presets))
	shorthandList := make(map[string][]string)
	for presetName, presetData := range cfg.Presets {
		definedPresetNames = append(definedPresetNames, presetName)

		for _, shorthand := range append(presetData.Shorthands, presetName) {
			if shorthand == "" {
				addErrorString(fmt.Sprintf(presetShorthandBlank, presetName))
			}

			if list, found := shorthandList[shorthand]; found {
				shorthandList[shorthand] = append(list, presetName)
			} else {
				shorthandList[shorthand] = []string{presetName}
			}
		}

		for format, defaultTools := range presetData.DefaultTools {
			isFormatKnown := mimetype.Lookup(format) != nil
			if !isFormatKnown {
				addErrorString(fmt.Sprintf(presetUnknownDefaultFormat, presetName, format))
			}

			for _, toolName := range defaultTools {
				tool, ok := cfg.Tools[toolName]
				if !ok {
					addErrorString(fmt.Sprintf(presetUnknownDefaultTool, presetName, format, toolName))
					continue
				}

				if _, ok := tool.Arguments[presetName]; !ok {
					addErrorString(fmt.Sprintf(presetDefaultToolWithNoArgs, presetName, toolName))
				}

				// Don't check for unknown formats to declutter
				if isFormatKnown && !slices.Contains(tool.SupportedFormats, format) {
					addErrorString(fmt.Sprintf(presetDefaultToolUnsupported, presetName, toolName, format))
				}
			}
		}
	}

	for shorthand, presets := range shorthandList {
		if len(presets) > 1 {
			addErrorString(fmt.Sprintf(presetShorthandConflict, shorthand, strings.Join(presets, ", ")))
		}
	}

	// tools
	for name, tool := range cfg.Tools {
		if tool.Command == "" {
			addErrorString(fmt.Sprintf(toolUndefinedCommand, name))
		}

		if len(tool.Platform) == 0 {
			addErrorString(fmt.Sprintf(toolUndefinedPlatform, name))
		} else {
			for _, platform := range tool.Platform {
				if !slices.Contains(Platforms, platform) {
					addErrorString(fmt.Sprintf(toolUnknownPlatform, name, platform))
				}
			}
		}

		if len(tool.SupportedFormats) == 0 {
			addErrorString(fmt.Sprintf(toolUndefinedFormat, name))
		} else {
			for _, fileFormat := range tool.SupportedFormats {
				if mimetype.Lookup(fileFormat) == nil {
					addErrorString(fmt.Sprintf(toolUnknownFileFormat, name, fileFormat))
				}
			}
		}

		if len(tool.Arguments) == 0 {
			addErrorString(fmt.Sprintf(toolUndefinedPresets, name))
		} else {
			for presetName := range tool.Arguments {
				if !slices.Contains(definedPresetNames, presetName) {
					addErrorString(fmt.Sprintf(toolUnknownPreset, name, presetName))
				}
			}
		}
	}

	return configErrors
}

func (cfg *Config) Cache() {
	if cfg.isCached {
		return
	}

	cfg.isCached = true

	cfg.cacheSupportedFileFormats()
	cfg.cacheSupportedFileExtensions()
	cfg.cacheAvailability()
}

func (cfg *Config) cacheSupportedFileFormats() {
	seen := slices.Collect(maps.Keys(cfg.MimeExtensions))
	for _, tool := range cfg.Tools {
		for _, mime := range tool.SupportedFormats {
			if slices.Contains(seen, mime) {
				continue
			}

			seen = append(seen, mime)
		}
	}

	seen = slices.DeleteFunc(seen, func(mime string) bool {
		return mimetype.Lookup(mime) == nil
	})

	cfg.supportedFileFormats = seen
}

func (cfg *Config) cacheSupportedFileExtensions() {
	mimeStrings := cfg.GetSupportedFileFormats()
	extensions := make(map[string][]string, len(mimeStrings))

	for _, mimeString := range mimeStrings {
		mime := mimetype.Lookup(mimeString)

		if mimeExtensions, ok := cfg.MimeExtensions[mimeString]; ok {
			extensions[mimeString] = mimeExtensions

			defaultExtension := mime.Extension()
			if !slices.Contains(mimeExtensions, defaultExtension) {
				extensions[mimeString] = append(extensions[mimeString], defaultExtension)
			}

			continue
		}

		extensions[mimeString] = []string{mime.Extension()}
	}

	cfg.supportedFileExtensions = extensions
}

func (cfg *Config) cacheAvailability() {
	availability := make(map[string]struct{}, len(cfg.Tools))

	for toolName, tool := range cfg.Tools {
		wrapper := cfg.QueryToolWrapper(tool, runtime.GOOS)
		if !slices.Contains(tool.Platform, runtime.GOOS) {
			_, err := exec.LookPath(wrapper)
			if err != nil {
				continue
			}
		}

		_, ok := FindExecutablePath(tool.Command, tool.Platform)
		if ok {
			availability[toolName] = struct{}{}
		}
	}

	cfg.toolAvailability = availability
}

func (ct *CompressionTool) Overwrites() bool {
	return ct.OutputMode == BatchOverwrite
}

func (ct *CompressionTool) CanBatchCompress() bool {
	return ct.OutputMode == BatchOverwrite
}

func (o *OutputMode) UnmarshalYAML(value *yaml.Node) error {
	var mode string
	if err := value.Decode(&mode); err != nil {
		return err
	}

	switch strings.ToLower(mode) {
	case "batch-overwrite":
		*o = BatchOverwrite
	case "input-output":
		*o = InputOutput
	case "stdout":
		*o = Stdout
	default:
		return fmt.Errorf("unknown output-mode %q", mode)
	}

	return nil
}

func (o OutputMode) MarshalYAML() (any, error) {
	var modeStr string
	switch o {
	case BatchOverwrite:
		modeStr = "batch-overwrite"
	case InputOutput:
		modeStr = "input-output"
	case Stdout:
		modeStr = "stdout"
	default:
		modeStr = "unknown"
	}

	return &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: modeStr,
		Tag:   "!!str",
	}, nil
}

func createUserConfigFilePath(warnLog *log.Logger) (configPath string, err error) {
	userConfig, err := os.UserConfigDir()
	if err != nil {
		warnLog.Printf("Cannot retrieve user config directory: %v\n", err)
		return "", err
	}

	appConfigDir := filepath.Join(userConfig, "compacty")

	const rwxr_xr_x = 0755
	err = os.MkdirAll(appConfigDir, rwxr_xr_x)
	if err != nil {
		warnLog.Printf("Cannot create config directory %s: %v\n", appConfigDir, err)
		return "", err
	}

	configPath = filepath.Join(appConfigDir, "config.yaml")
	return configPath, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
