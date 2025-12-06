package config_test

import (
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ArrayNone/compacty/internal/config"

	"go.yaml.in/yaml/v3"
)

var validPreset = map[string]config.Preset{
	"default": {
		Description: "desc",
		Shorthands:  []string{"def"},
		DefaultTools: map[string][]string{
			"text/plain": {"cat"},
		},
	},

	"minimal": {
		Description:  "",
		Shorthands:   []string{},
		DefaultTools: map[string][]string{},
	},
}

var validTool = map[string]*config.ToolConfig{
	"cat": {
		Arguments: map[string][]string{"default": {"-u"}, "minimal": {}},
		CompressionTool: config.CompressionTool{
			Command:          "cat",
			Platform:         []string{"linux"},
			SupportedFormats: []string{"text/plain"},
			OutputMode:       config.Stdout,
		},
	},
}

var validWrapper = map[string]map[string]string{
	"linux": {
		"windows": "wine",
	},

	"windows": {
		"linux": "wsl",
	},
}

var validMimeExtensions = map[string][]string{
	"text/plain": {".txt"},

	"application/json": {".json"},
}

var validConfig = config.Config{
	DefaultPreset: "default",

	Presets:        validPreset,
	Tools:          validTool,
	Wrappers:       validWrapper,
	MimeExtensions: validMimeExtensions,
}

func TestConfig_Decode(t *testing.T) {
	validConfig.Cache()
	t.Run("basic config reencoded", func(t *testing.T) {
		data, err := yaml.Marshal(validConfig)
		if err != nil {
			t.Fatal("error occurred while encoding:", err.Error())
		}

		var reencoded config.Config
		err = yaml.Unmarshal(data, &reencoded)
		if err != nil {
			t.Fatal("error occurred while decoding:", err.Error())
		}

		reencoded.Cache()
		errs := reencoded.Validate()
		if len(errs) > 0 {
			t.Error("validation failed:\n" + errors.Join(errs...).Error())
		}
	})
}

func TestConfig_QueryPreset(t *testing.T) {
	validConfig.Cache()
	t.Run("basic preset", func(t *testing.T) {
		queriedPreset, isShorthand := config.QueryPreset(validPreset, "default")
		if queriedPreset != "default" {
			t.Errorf("config contains \"default\" as a preset. trying to query default, got: %q", queriedPreset)
		}

		if isShorthand {
			t.Error("\"default\" is considered a shorthand, but it actually isn't")
		}
	})

	t.Run("basic preset (shorthand)", func(t *testing.T) {
		queriedShorthand, isShorthand := config.QueryPreset(validPreset, "def")
		if queriedShorthand != "default" {
			t.Errorf("config contains \"def\" as a shorthand for \"default\". trying to query default, got: %q", queriedShorthand)
		}

		if !isShorthand {
			t.Error("\"def\" is not considered a shorthand, but it actually is")
		}
	})
}

func TestConfig_GetSupportedFileFormats(t *testing.T) {
	validConfig.Cache()
	t.Run("basic config", func(t *testing.T) {
		formats := validConfig.GetSupportedFileFormats()

		slices.Sort(formats)
		expectedMimes := []string{"application/json", "text/plain"}

		if !slices.Equal(formats, expectedMimes) {
			t.Errorf("expected %v, got: %v", expectedMimes, formats)
		}
	})

	t.Run("invalid file format", func(t *testing.T) {
		nonexistingMimeConfig := &config.Config{
			MimeExtensions: map[string][]string{"invalid/mime": {".xyz"}},
		}

		fileFormats := nonexistingMimeConfig.GetSupportedFileFormats()
		if !slices.Equal(fileFormats, []string{}) {
			t.Error("invalid mime gets returned, but shouldn't")
		}
	})
}

func TestConfig_GetSupportedFileExtensions(t *testing.T) {
	validConfig.Cache()
	t.Run("basic config", func(t *testing.T) {
		formats := validConfig.GetSupportedFileExtensions()

		expectedMimes := map[string][]string{"application/json": {".json"}, "text/plain": {".txt"}}
		if !maps.EqualFunc(formats, expectedMimes, func(a []string, b []string) bool {
			return slices.Equal(a, b)
		},
		) {
			t.Errorf("expected %v, got: %v", expectedMimes, formats)
		}
	})

	t.Run("invalid file format", func(t *testing.T) {
		invalidMimeConfig := &config.Config{
			MimeExtensions: map[string][]string{"invalid/mime": {".xyz"}},
		}

		fileFormats := invalidMimeConfig.GetSupportedFileExtensions()
		if _, ok := fileFormats["invalid/mime"]; ok {
			t.Error("invalid mime gets returned, but shouldn't")
		}
	})

	t.Run("valid file format with extension undefined in mime-extensions should have a default extension", func(t *testing.T) {
		nonexistingMimeConfig := &config.Config{
			MimeExtensions: map[string][]string{"image/png": {}}, // This fails validation normally
		}

		fileExtensions := nonexistingMimeConfig.GetSupportedFileExtensions()
		if exts, ok := fileExtensions["image/png"]; ok {
			extension := exts[0]
			if extension != ".png" {
				t.Errorf("wrong extension for image/png, expected .png, got %s", extension)
			}

			return
		}

		t.Error("mime is valid but cannot be found")
	})
}

func TestConfig_GetToolConfigFromNames(t *testing.T) {
	validConfig.Cache()
	t.Run("basic config", func(t *testing.T) {
		tools := []string{"cat"}
		toolConfig := validConfig.GetToolConfigFromNames(tools)
		if !reflect.DeepEqual(toolConfig, validTool) {
			t.Errorf("expected %v, got: %v", toolConfig, validTool)
		}
	})
}

func TestConfig_DefaultConfig(t *testing.T) {
	t.Run("decode default config", func(t *testing.T) {
		defaultStr := config.GetDefaultConfigStr()

		var cfg config.Config
		err := yaml.Unmarshal([]byte(defaultStr), &cfg)
		if err != nil {
			t.Fatal("error occurred while decoding:", err.Error())
		}

		errs := cfg.Validate()
		if len(errs) > 0 {
			t.Fatal("validation failed:\n" + errors.Join(errs...).Error())
		}
	})
}

func TestConfig_Validate(t *testing.T) {
	type errorTestCase struct {
		name   string
		config config.Config

		wantError string
	}

	errorTestCases := []errorTestCase{
		{
			name: "missing default-preset",
			config: config.Config{
				DefaultPreset: "",

				Presets:  validPreset,
				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "default-preset is not defined",
		},

		{
			name: "unknown default-preset",
			config: config.Config{
				DefaultPreset: "nope",

				Presets:  validPreset,
				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "default-preset is an undefined preset: nope",
		},

		{
			name: "unknown file format in mime-extensions",
			config: config.Config{
				DefaultPreset: "default",

				Presets:  validPreset,
				Tools:    validTool,
				Wrappers: validWrapper,

				MimeExtensions: map[string][]string{
					"invalid/mime": {".abc"},
				},
			},
			wantError: "mime-extensions: \"invalid/mime\" is an unknown file format",
		},
		{
			name: "undefined extensions in mime-extensions",
			config: config.Config{
				DefaultPreset: "default",

				Presets:  validPreset,
				Tools:    validTool,
				Wrappers: validWrapper,

				MimeExtensions: map[string][]string{
					"text/plain": {},
				},
			},
			wantError: "mime-extensions: \"text/plain\" has no defined file extensions",
		},

		{
			name: "unknown platform in wrapper",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools:   validTool,
				Wrappers: map[string]map[string]string{
					"hal9000": {
						"windows": "wine",
					},
				},
			},
			wantError: "wrapper: unknown platform defined: hal9000",
		},
		{
			name: "unknown platform in inner wrapper",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools:   validTool,
				Wrappers: map[string]map[string]string{
					"linux": {
						"hal9000": "",
					},
				},
			},
			wantError: "wrapper: unknown platform defined in \"linux\": hal9000",
		},
		{
			name: "blank wrapper command",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools:   validTool,
				Wrappers: map[string]map[string]string{
					"linux": {
						"windows": "",
					},
				},
			},
			wantError: "wrapper: blank command defined in \"windows\", then \"linux\"",
		},

		{
			name: "conflicting shorthand in presets",
			config: config.Config{
				DefaultPreset: "default",

				Presets: map[string]config.Preset{
					"one": {Description: "desc", Shorthands: []string{"a"}},
					"two": {Description: "desc", Shorthands: []string{"a"}},
				},
				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "preset: conflicting shorthand \"a\" on multiple presets:",
		},
		{
			name: "blank shorthand in preset",
			config: config.Config{
				DefaultPreset: "blank",

				Presets: map[string]config.Preset{
					"blank": {Description: "blank shorthand", Shorthands: []string{""}},
				},

				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "preset: shorthand on \"blank\" cannot be a blank name",
		},
		{
			name: "unknown tool defined on default-tools in preset",
			config: config.Config{
				DefaultPreset: "unknown",

				Presets: map[string]config.Preset{
					"unknown": {Description: "", DefaultTools: map[string][]string{
						"image/png": {"obliterator"},
					}},
				},

				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "preset: \"unknown\" included an undefined tool on default-tools at \"image/png\": obliterator",
		},
		{
			name: "tool on default-tools with undefined arguments",
			config: config.Config{
				DefaultPreset: "default",

				Presets: map[string]config.Preset{
					"unknown": {Description: "", DefaultTools: map[string][]string{
						"text/plain": {"cat"},
					}},
				},

				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "preset: \"unknown\" included tool \"cat\" on default-tools with undefined arguments for this preset",
		},
		{
			name: "tool on default-tools with unsupported file format",
			config: config.Config{
				DefaultPreset: "default",

				Presets: map[string]config.Preset{
					"unknown": {Description: "", DefaultTools: map[string][]string{
						"image/png": {"cat"},
					}},
				},

				Tools:    validTool,
				Wrappers: validWrapper,
			},
			wantError: "preset: \"unknown\" included tool \"cat\" on default-tools for image/png, which does not support this file format",
		},

		{
			name: "undefined tool command",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"default": {"-a"}},
						CompressionTool: config.CompressionTool{
							Command:          "",
							Platform:         []string{"linux"},
							SupportedFormats: []string{"text/plain"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has no command defined",
		},
		{
			name: "undefined tool platform",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"default": {"-a"}},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{},
							SupportedFormats: []string{"text/plain"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has no platforms defined",
		},
		{
			name: "undefined tool supported file formats",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"default": {"-a"}},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{"linux"},
							SupportedFormats: []string{},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has no supported-formats defined",
		},
		{
			name: "undefined tool argument presets",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{"linux"},
							SupportedFormats: []string{"text/plain"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has no arguments defined",
		},
		{
			name: "unknown platform on tool",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"default": {"-a"}},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{"hal9000"},
							SupportedFormats: []string{"text/plain"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has unknown platform defined: hal9000",
		},
		{
			name: "unknown argument preset on tool",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"fast": {"-a"}},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{"linux"},
							SupportedFormats: []string{"text/plain"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has unknown preset defined in arguments: fast",
		},
		{
			name: "unknown file format on tool",
			config: config.Config{
				DefaultPreset: "default",

				Presets: validPreset,
				Tools: map[string]*config.ToolConfig{
					"false": {
						Arguments: map[string][]string{"default": {}},
						CompressionTool: config.CompressionTool{
							Command:          "false",
							Platform:         []string{"linux"},
							SupportedFormats: []string{"invalid/mime"},
						},
					},
				},
				Wrappers: validWrapper,
			},
			wantError: "tool: \"false\" has unknown file format defined: invalid/mime",
		},
	}

	t.Run("valid preset", func(t *testing.T) {
		errs := validConfig.Validate()
		if len(errs) > 0 {
			t.Fatalf("expected no error, but got:\n%v", errors.Join(errs...))
		}
	})

	for _, testCase := range errorTestCases {
		t.Run(testCase.name, func(t *testing.T) {
			errs := testCase.config.Validate()
			if len(errs) == 0 {
				t.Fatal("expected errors, got no error")
			}

			fullErrorString := errors.Join(errs...).Error()
			if !strings.Contains(fullErrorString, testCase.wantError) {
				t.Fatalf("full error:\n%s\n\ndoes not contain expected error:\n%s", fullErrorString, testCase.wantError)
			}
		})
	}
}
