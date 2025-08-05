// Copyright 2023 the Drone Authors. All rights reserved.
// Use of this source code is governed by the Blue Oak Model License
// that can be found in the LICENSE file.

package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/sirupsen/logrus"
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
// The content is JSON-escaped to handle multi-line YAML properly in environment variables.
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

	// Export the YAML content with JSON escaping for proper environment variable handling
	// First validate that content is valid UTF-8
	if !utf8.Valid(content) {
		logrus.Warnf("YAML file contains invalid UTF-8 sequences, PLUGIN_HARNESS_YAML not exported")
		return nil
	}

	// Use encoder with HTML escaping disabled for cleaner output
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false) // Prevents <> from becoming \u003c\u003e
	if err := encoder.Encode(string(content)); err != nil {
		return fmt.Errorf("failed to marshal YAML content: %w", err)
	}

	// json.NewEncoder.Encode adds a trailing newline, remove it
	jsonBytes := buf.Bytes()
	if len(jsonBytes) > 0 && jsonBytes[len(jsonBytes)-1] == '\n' {
		jsonBytes = jsonBytes[:len(jsonBytes)-1]
	}

	// Safety check: should always return at least `""` (2 characters)
	if len(jsonBytes) < 2 {
		return fmt.Errorf("unexpected JSON marshal result: %s", string(jsonBytes))
	}

	// Remove the surrounding quotes that JSON encoding adds
	escapedContent := string(jsonBytes[1 : len(jsonBytes)-1])

	// Additional safety: check if escaped content would be too large for env vars
	// Most systems support ~2MB, but use 3MB to be consistent with original requirement
	if len(escapedContent) > 3*1024*1024 { // 3MB limit for env var
		logrus.Warnf("Escaped YAML content size (%d bytes) exceeds 3MB env var limit, PLUGIN_HARNESS_YAML not exported", len(escapedContent))
		return nil
	}

	if err := WriteEnvToFile("PLUGIN_HARNESS_YAML", escapedContent); err != nil {
		return fmt.Errorf("failed to export YAML content to environment: %w", err)
	}

	logrus.Infof("YAML content (%d bytes) exported successfully via PLUGIN_HARNESS_YAML", len(content))
	return nil
}
