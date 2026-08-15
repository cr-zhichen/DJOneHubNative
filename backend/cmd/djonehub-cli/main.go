package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var version = "dev"

type globalOptions struct {
	Format     string
	Pretty     bool
	SocketPath string
	ConfigPath string
	Timeout    time.Duration
	Warnings   []warningPayload
}

type commandResult struct {
	Command   string
	Data      any
	RequestID string
	Streamed  bool
}

type commandError struct {
	Code        string
	Message     string
	Recoverable bool
	Details     any
	RequestID   string
	ExitCode    int
}

type outputEnvelope struct {
	OK        bool             `json:"ok"`
	Command   string           `json:"command,omitempty"`
	Data      any              `json:"data,omitempty"`
	RequestID string           `json:"request_id,omitempty"`
	Error     *errorPayload    `json:"error,omitempty"`
	Warnings  []warningPayload `json:"warnings,omitempty"`
}

type warningPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type errorPayload struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
	Details     any    `json:"details,omitempty"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(execute(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func execute(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, remaining, err := parseGlobalOptions(args)
	if err != nil {
		return emitError(stderr, globalOptions{Format: "json"}, "", err)
	}
	options.Warnings = cliRuntimeWarnings()
	result, commandErr := dispatch(ctx, options, remaining, stdout)
	if commandErr != nil {
		return emitError(stderr, options, result.Command, commandErr)
	}
	if result.Streamed {
		return 0
	}
	if err := emitJSON(stdout, options.Pretty, outputEnvelope{
		OK: true, Command: result.Command, Data: result.Data, RequestID: result.RequestID,
		Warnings: options.Warnings,
	}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func parseGlobalOptions(args []string) (globalOptions, []string, *commandError) {
	options := globalOptions{
		Format:     "json",
		SocketPath: defaultSocketPath(),
		ConfigPath: defaultConfigPath(),
		Timeout:    3 * time.Minute,
	}
	remaining := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--pretty" {
			options.Pretty = true
			continue
		}
		name, value, matched := splitGlobalFlag(argument)
		if !matched {
			remaining = append(remaining, argument)
			continue
		}
		if value == "" {
			index++
			if index >= len(args) {
				return options, nil, usageError(name + " requires a value")
			}
			value = args[index]
		}
		switch name {
		case "--format":
			options.Format = strings.ToLower(value)
		case "--socket":
			options.SocketPath = value
		case "--config":
			options.ConfigPath = value
		case "--timeout":
			duration, err := time.ParseDuration(value)
			if err != nil || duration < time.Second || duration > 10*time.Minute {
				return options, nil, usageError("--timeout must be between 1s and 10m")
			}
			options.Timeout = duration
		}
	}
	if options.Format != "json" && options.Format != "ndjson" {
		return options, nil, usageError("--format must be json or ndjson")
	}
	return options, remaining, nil
}

func splitGlobalFlag(argument string) (string, string, bool) {
	for _, name := range []string{"--format", "--socket", "--config", "--timeout"} {
		if argument == name {
			return name, "", true
		}
		if strings.HasPrefix(argument, name+"=") {
			return name, strings.TrimPrefix(argument, name+"="), true
		}
	}
	return "", "", false
}

func emitError(writer io.Writer, options globalOptions, command string, err *commandError) int {
	if err == nil {
		err = &commandError{Code: "unknown_error", Message: "unknown error", ExitCode: 1}
	}
	_ = emitJSON(writer, options.Pretty, outputEnvelope{
		OK: false, Command: command, RequestID: err.RequestID,
		Warnings: options.Warnings,
		Error: &errorPayload{
			Code: err.Code, Message: err.Message, Recoverable: err.Recoverable, Details: err.Details,
		},
	})
	if err.ExitCode <= 0 {
		return 1
	}
	return err.ExitCode
}

func emitJSON(writer io.Writer, pretty bool, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", indentIf(pretty))
	return encoder.Encode(value)
}

func indentIf(pretty bool) string {
	if pretty {
		return "  "
	}
	return ""
}

func usageError(message string) *commandError {
	return &commandError{Code: "invalid_arguments", Message: message, ExitCode: 2}
}

func parseBoundedInt(value string, minimum, maximum int, name string) (int, *commandError) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, usageError(fmt.Sprintf("%s must be between %d and %d", name, minimum, maximum))
	}
	return parsed, nil
}
