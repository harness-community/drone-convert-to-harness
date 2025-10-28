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

	"github.com/google/shlex"
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

// parseOptionsAndExtractRepoName splits PLUGIN_OPTIONS and extracts/removes --repo-name
func parseOptionsAndExtractRepoName(options string) ([]string, string, error) {
    if strings.TrimSpace(options) == "" {
        return nil, "", nil
    }

    tokens, err := shlex.Split(options)
    if err != nil {
        return nil, "", err
    }

    filtered := make([]string, 0, len(tokens))
    var repoName string

    for i := 0; i < len(tokens); i++ {
        t := tokens[i]
        if strings.HasPrefix(t, "--repo-name=") {
            repoName = strings.TrimPrefix(t, "--repo-name=")
            continue
        }
        if t == "--repo-name" {
            if i+1 < len(tokens) {
                repoName = tokens[i+1]
                i++
            } else {
                logrus.Warnf("--repo-name provided without a value; ignoring")
            }
            continue
        }
        filtered = append(filtered, t)
    }

    return filtered, repoName, nil
}

// injectRepoNameIntoYAML adds/overrides pipeline.properties.ci.codebase.repoName in the YAML file
func injectRepoNameIntoYAML(filePath, repoName string) error {
    if strings.TrimSpace(repoName) == "" {
        return nil
    }

    data, err := os.ReadFile(filePath)
    if err != nil {
        return fmt.Errorf("failed to read YAML for injection: %w", err)
    }

    var doc yaml.Node
    if err := yaml.Unmarshal(data, &doc); err != nil {
        return fmt.Errorf("failed to parse YAML for injection: %w", err)
    }

    if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
        return fmt.Errorf("unexpected YAML structure: missing document root")
    }

    root := doc.Content[0]
    if root.Kind != yaml.MappingNode {
        return fmt.Errorf("unexpected YAML structure: root is not a mapping")
    }

    pipeline := getOrCreateMap(root, "pipeline")
    properties := getOrCreateMap(pipeline, "properties")
    ci := getOrCreateMap(properties, "ci")
    codebase := getOrCreateMap(ci, "codebase")

    setMapScalar(codebase, "repoName", repoName)

    out, err := yaml.Marshal(&doc)
    if err != nil {
        return fmt.Errorf("failed to marshal updated YAML: %w", err)
    }

    if err := os.WriteFile(filePath, out, 0644); err != nil {
        return fmt.Errorf("failed to write updated YAML: %w", err)
    }

    logrus.Infof("Injected repoName=%q into YAML codebase", repoName)
    return nil
}

// getOrCreateMap finds the map value for key under mapNode, creating it if absent.
func getOrCreateMap(mapNode *yaml.Node, key string) *yaml.Node {
    if mapNode == nil || mapNode.Kind != yaml.MappingNode {
        // Replace non-mapping with an empty mapping for safety
        *mapNode = yaml.Node{Kind: yaml.MappingNode}
    }
    // Search for existing key
    for i := 0; i < len(mapNode.Content); i += 2 {
        k := mapNode.Content[i]
        v := mapNode.Content[i+1]
        if k.Value == key {
            if v.Kind != yaml.MappingNode {
                // Replace non-mapping value with a mapping
                newMap := &yaml.Node{Kind: yaml.MappingNode}
                mapNode.Content[i+1] = newMap
                return newMap
            }
            return v
        }
    }
    // Not found, append new mapping
    keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
    valNode := &yaml.Node{Kind: yaml.MappingNode}
    mapNode.Content = append(mapNode.Content, keyNode, valNode)
    return valNode
}

// setMapScalar sets or adds a scalar value under key in a mapping node.
func setMapScalar(mapNode *yaml.Node, key, value string) {
    if mapNode == nil || mapNode.Kind != yaml.MappingNode {
        return
    }
    for i := 0; i < len(mapNode.Content); i += 2 {
        if mapNode.Content[i].Value == key {
            mapNode.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
            return
        }
    }
    // Not found, append new key/value
    k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
    v := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
    mapNode.Content = append(mapNode.Content, k, v)
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

	// Parse PLUGIN_OPTIONS and extract --repo-name (if present)
	optionTokens, repoName, err := parseOptionsAndExtractRepoName(args.Options)
	if err != nil {
		return fmt.Errorf("failed to parse PLUGIN_OPTIONS: %w", err)
	}

	// Get path to the go-convert binary
	converterBin, err := findConverterBinary()
	if err != nil {
		return fmt.Errorf("failed to find go-convert binary: %w", err)
	}

	// Build arguments for go-convert (with filtered options)
	cmdArgs := buildCommandArgs(args, optionTokens)

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

	// Inject repoName into YAML, if provided via --repo-name
	if repoName != "" {
		if err := injectRepoNameIntoYAML(args.HarnessOutputYAMLPath, repoName); err != nil {
			logrus.Warnf("Failed to inject repoName into YAML: %v", err)
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
func buildCommandArgs(args Args, optionTokens []string) []string {
	var cmdArgs []string

	// Add the CI provider command first
	cmdArgs = append(cmdArgs, args.CIProvider)

	// Add any additional options provided by the user (already tokenized and filtered)
	if len(optionTokens) > 0 {
		cmdArgs = append(cmdArgs, optionTokens...)
	}

	// Add downgrade flag if needed
	if args.Downgrade {
		cmdArgs = append(cmdArgs, "--downgrade")
	}

	// Add source file path (must be the last argument)
	cmdArgs = append(cmdArgs, args.SourceYAML)

	return cmdArgs
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
