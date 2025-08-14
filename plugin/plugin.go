// Copyright 2023 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

// Args provides plugin execution arguments.
type Args struct {
	// Level defines the plugin log level.
	Level string `envconfig:"PLUGIN_LOG_LEVEL"`

	// CI provider type
	CIProvider string `envconfig:"PLUGIN_CI_PROVIDER" default:"drone"`

	// Source YAML path
	SourceYAML string `envconfig:"PLUGIN_SOURCE_YAML"`

	// Output path for Harness YAML
	HarnessOutputYAMLPath string `envconfig:"PLUGIN_HARNESS_OUTPUT_YAML_PATH"`

	// Whether to downgrade the YAML
	Downgrade bool `envconfig:"PLUGIN_DOWNGRADE" default:"true"`

	// Additional CLI options to pass to go-convert
	Options string `envconfig:"PLUGIN_OPTIONS"`
}

// buildOverride represents a post-conversion override for codebase.build
type buildOverride struct {
	Kind  string // "branch" | "tag" | "PR"
	Value string
}

// Exec executes the plugin.
func Exec(args Args) error {
	// Validate inputs
	if err := validate(args); err != nil {
		return err
	}

	// Set default output path if not provided
	if args.HarnessOutputYAMLPath == "" {
		// Get workspace from environment or use current directory
		workspace := os.Getenv("DRONE_WORKSPACE")
		if workspace == "" {
			var err error
			workspace, err = os.Getwd()
			if err != nil {
				return fmt.Errorf("failed to get current directory: %w", err)
			}
		}
		args.HarnessOutputYAMLPath = filepath.Join(workspace, "convert", "harness_pipeline.yaml")
	}

	// Create output directory if it doesn't exist
	outputDir := filepath.Dir(args.HarnessOutputYAMLPath)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// All CLI options should be specified via PLUGIN_OPTIONS

	// Parse build override flags from options and sanitize the options passed to go-convert
	sanitizedOptions, override, warnMsg := parseBuildOverrideAndSanitizeOptions(args.Options)
	if warnMsg != "" {
		logrus.Warn(warnMsg)
	}
	args.Options = sanitizedOptions

	// Get path to the go-convert binary
	converterBin, err := findConverterBinary()
	if err != nil {
		return fmt.Errorf("failed to find go-convert binary: %w", err)
	}

	// Build arguments for go-convert
	cmdArgs := buildCommandArgs(args)

	// Execute go-convert
	logrus.Infof("Executing go-convert binary: %s\n", converterBin)
	logrus.Infof("Arguments: %s\n", strings.Join(cmdArgs, " "))

	// Create output file
	outputFile, err := os.Create(args.HarnessOutputYAMLPath)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Set up command
	cmd := exec.Command(converterBin, cmdArgs...)
	cmd.Stderr = os.Stderr  // Send stderr to console
	cmd.Stdout = outputFile // Redirect stdout to the output file

	// Execute command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("go-convert execution failed: %w", err)
	}

	// Verify the output file was created
	if _, err := os.Stat(args.HarnessOutputYAMLPath); os.IsNotExist(err) {
		return fmt.Errorf("output file was not created: %s", args.HarnessOutputYAMLPath)
	}

	// If a build override was provided, update the generated YAML before exporting
	if override != nil {
		if err := updateCodebaseBuildInYAML(args.HarnessOutputYAMLPath, *override); err != nil {
			logrus.Warnf("Failed to apply build override to YAML: %v", err)
		} else {
			logrus.Infof("Applied build override to YAML: type=%s", override.Kind)
		}
	}

	// Set output variable
	if err := WriteEnvToFile("PLUGIN_HARNESS_YAML_PATH", args.HarnessOutputYAMLPath); err != nil {
		return fmt.Errorf("failed to set output variable: %w", err)
	}

	// Export the converted YAML content directly via PLUGIN_HARNESS_YAML
	if err := exportYAMLContent(args.HarnessOutputYAMLPath); err != nil {
		// Don't fail the plugin, just log a warning to maintain existing behavior
		logrus.Warnf("Failed to export YAML content: %v", err)
	}

	logrus.Infof("Conversion completed successfully\n")
	return nil
}

// Finds the go-convert binary in its standard location
func findConverterBinary() (string, error) {
	// In the container, the binary is installed at a known location
	// based on the operating system
	switch runtime.GOOS {
	case "windows":
		return "go-convert.exe", nil
	default: // linux, darwin
		return "/bin/go-convert", nil
	}
}

// buildCommandArgs builds the command line arguments for go-convert
func buildCommandArgs(args Args) []string {
	var cmdArgs []string

	// Add the CI provider command first
	cmdArgs = append(cmdArgs, args.CIProvider)

	// Add any additional options provided by the user
	if args.Options != "" {
		// Split the options string on whitespace
		cmdArgs = append(cmdArgs, strings.Fields(args.Options)...)
	}

	// Add downgrade flag if needed
	if args.Downgrade {
		cmdArgs = append(cmdArgs, "--downgrade")
	}

	// Add source file path (must be the last argument)
	cmdArgs = append(cmdArgs, args.SourceYAML)

	return cmdArgs
}

// parseBuildOverrideAndSanitizeOptions scans the options string for plugin-only build
// override flags and removes them from the options passed to go-convert.
// Supported flags:
//   --build-branch <value> | --build-branch=<value>
//   --build-tag <value>    | --build-tag=<value>
//   --build-pr <value>     | --build-pr=<value>
// If multiple are provided, the last one wins and a warning message is returned.
func parseBuildOverrideAndSanitizeOptions(options string) (sanitized string, override *buildOverride, warnMsg string) {
    if strings.TrimSpace(options) == "" {
        return "", nil, ""
    }

    tokens := strings.Fields(options)
    var sanitizedTokens []string
    var foundOverride *buildOverride

    // Helper to set/override with warning if applicable
    setOverride := func(kind, value string) {
        if value == "" {
            return
        }
        if foundOverride != nil {
            warnMsg = "multiple --build-* flags provided; using the last one"
        }
        foundOverride = &buildOverride{Kind: kind, Value: value}
    }

    skipNext := false
    for i := 0; i < len(tokens); i++ {
        if skipNext {
            skipNext = false
            continue
        }

        t := tokens[i]
        // Handle --flag=value form
        if strings.HasPrefix(t, "--build-branch=") {
            setOverride("branch", strings.TrimPrefix(t, "--build-branch="))
            continue
        }
        if strings.HasPrefix(t, "--build-tag=") {
            setOverride("tag", strings.TrimPrefix(t, "--build-tag="))
            continue
        }
        if strings.HasPrefix(t, "--build-pr=") {
            setOverride("PR", strings.TrimPrefix(t, "--build-pr="))
            continue
        }

        // Handle --flag value form
        if t == "--build-branch" || t == "--build-tag" || t == "--build-pr" {
            var val string
            if i+1 < len(tokens) {
                next := tokens[i+1]
                // Only treat as value if the next token does not look like another flag
                if !strings.HasPrefix(next, "-") {
                    val = next
                    skipNext = true
                }
            }
            switch t {
            case "--build-branch":
                setOverride("branch", val)
            case "--build-tag":
                setOverride("tag", val)
            case "--build-pr":
                setOverride("PR", val)
            }
            continue
        }

        // Keep everything else
        sanitizedTokens = append(sanitizedTokens, t)
    }

    return strings.Join(sanitizedTokens, " "), foundOverride, warnMsg
}

// updateCodebaseBuildInYAML opens the generated YAML and replaces
// pipeline.properties.ci.codebase.build with the desired structure.
func updateCodebaseBuildInYAML(filePath string, override buildOverride) error {
    if filePath == "" {
        return fmt.Errorf("yaml file path cannot be empty")
    }

    content, err := os.ReadFile(filePath)
    if err != nil {
        return fmt.Errorf("failed to read YAML file: %w", err)
    }

    var root map[string]interface{}
    if err := yaml.Unmarshal(content, &root); err != nil {
        return fmt.Errorf("failed to parse YAML: %w", err)
    }

    // Navigate to pipeline.properties.ci.codebase
    pipeline, ok := root["pipeline"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("pipeline root not found or not an object")
    }
    properties, ok := pipeline["properties"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("pipeline.properties not found or not an object")
    }
    ci, ok := properties["ci"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("pipeline.properties.ci not found or not an object")
    }
    codebase, ok := ci["codebase"].(map[string]interface{})
    if !ok {
        return fmt.Errorf("pipeline.properties.ci.codebase not found or not an object")
    }

    // Build the override structure
    buildNode := map[string]interface{}{}
    switch override.Kind {
    case "branch":
        buildNode["type"] = "branch"
        buildNode["spec"] = map[string]interface{}{"branch": override.Value}
    case "tag":
        buildNode["type"] = "tag"
        buildNode["spec"] = map[string]interface{}{"tag": override.Value}
    case "PR":
        buildNode["type"] = "PR"
        buildNode["spec"] = map[string]interface{}{"number": override.Value}
    default:
        return fmt.Errorf("unknown build override kind: %s", override.Kind)
    }

    codebase["build"] = buildNode

    // Marshal back and write
    updated, err := yaml.Marshal(root)
    if err != nil {
        return fmt.Errorf("failed to marshal updated YAML: %w", err)
    }
    if err := os.WriteFile(filePath, updated, 0644); err != nil {
        return fmt.Errorf("failed to write updated YAML: %w", err)
    }
    return nil
}

// validate validates the plugin arguments.
func validate(args Args) error {
	if args.SourceYAML == "" {
		return fmt.Errorf("source YAML path not provided")
	}

	// Check if source YAML exists
	if _, err := os.Stat(args.SourceYAML); os.IsNotExist(err) {
		return fmt.Errorf("source YAML file does not exist: %s", args.SourceYAML)
	}

	return nil
}

// WriteEnvToFile writes environment variables to the Drone output file.
func WriteEnvToFile(key, value string) error {
	outputFile, err := os.OpenFile(os.Getenv("DRONE_OUTPUT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}

	defer outputFile.Close()

	_, err = fmt.Fprintf(outputFile, "%s=%s\n", key, value)
	if err != nil {
		return fmt.Errorf("failed to write to env: %w", err)
	}

	return nil
}

// exportYAMLContent reads the generated YAML file and exports its content via PLUGIN_HARNESS_YAML
func exportYAMLContent(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Check if file exists and get basic info
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("failed to get file info for %s: %w", filePath, err)
	}

	// Handle empty files
	if fileInfo.Size() == 0 {
		logrus.Infof("YAML file is empty, exporting empty PLUGIN_HARNESS_YAML")
		return WriteEnvToFile("PLUGIN_HARNESS_YAML", "")
	}

	// Read the generated YAML file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read generated YAML file %s: %w", filePath, err)
	}

	// Validate that content is valid UTF-8
	if !utf8.Valid(content) {
		logrus.Warnf("YAML file contains invalid UTF-8 sequences, PLUGIN_HARNESS_YAML not exported")
		return nil
	}

	// Base64 encode the YAML content for safe environment variable storage
	encodedContent := base64.StdEncoding.EncodeToString(content)

	// Check if encoded content would be too large for env vars
	if len(encodedContent) > 3*1024*1024 { // 3MB limit for env var
		logrus.Warnf("Encoded YAML content size (%d bytes) exceeds 3MB env var limit, PLUGIN_HARNESS_YAML not exported", len(encodedContent))
		return nil
	}

	if err := WriteEnvToFile("PLUGIN_HARNESS_YAML", encodedContent); err != nil {
		return fmt.Errorf("failed to export YAML content to environment: %w", err)
	}

	logrus.Infof("YAML content (%d bytes) exported successfully via PLUGIN_HARNESS_YAML", len(content))
	return nil
}
