// Copyright (c) 2025, NVIDIA CORPORATION.  All rights reserved.
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package logger

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"strings"
	"time"
)

// Level represents logging severity
type Level int

const (
	// LevelTrace is for very detailed logs
	LevelTrace Level = iota
	// LevelDebug is for debug information
	LevelDebug
	// LevelInfo is for informational messages
	LevelInfo
	// LevelWarn is for warning messages
	LevelWarn
	// LevelError is for error messages
	LevelError
	// LevelFatal is for fatal errors that cause exit
	LevelFatal
)

// String returns the string representation of a log level
func (l Level) String() string {
	switch l {
	case LevelTrace:
		return "TRACE"
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	case LevelFatal:
		return "FATAL"
	default:
		return "UNKNOWN"
	}
}

// ParseLevel converts a string to a Level
func ParseLevel(s string) Level {
	switch strings.ToUpper(s) {
	case "TRACE":
		return LevelTrace
	case "DEBUG":
		return LevelDebug
	case "INFO":
		return LevelInfo
	case "WARN", "WARNING":
		return LevelWarn
	case "ERROR":
		return LevelError
	case "FATAL":
		return LevelFatal
	default:
		return LevelInfo
	}
}

// Interface defines logging methods matching toolkit patterns
type Interface interface {
	// Legacy methods for compatibility
	Infof(format string, args ...interface{})
	Warningf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Debugf(format string, args ...interface{})

	// New structured methods
	WithField(key string, value interface{}) Interface
	WithFields(fields map[string]interface{}) Interface
	WithError(err error) Interface

	// Level-based logging
	Trace(msg string)
	Debug(msg string)
	Info(msg string)
	Warn(msg string)
	Error(msg string)
	Fatal(msg string)

	// Printf-style with levels
	Tracef(format string, args ...interface{})
}

// Format represents the output format
type Format string

const (
	// FormatText is human-readable text format
	FormatText Format = "text"
	// FormatJSON is JSON format for structured logging
	FormatJSON Format = "json"
)

// Config holds logger configuration
type Config struct {
	Level   Level
	Format  Format
	Output  io.Writer
	Prefix  string
	Verbose bool // Legacy support
	Color   bool // Enable color output for text format
}

// logger implements Interface
type logger struct {
	config Config
	fields map[string]interface{}
}

// New creates a new logger instance with default config
func New(prefix string, verbose bool) Interface {
	level := LevelInfo
	if verbose {
		level = LevelDebug
	}

	return NewWithConfig(Config{
		Level:   level,
		Format:  FormatText,
		Output:  os.Stderr,
		Prefix:  prefix,
		Verbose: verbose,
		Color:   isTerminal(),
	})
}

// NewWithConfig creates a new logger with custom configuration
func NewWithConfig(config Config) Interface {
	return &logger{
		config: config,
		fields: make(map[string]interface{}),
	}
}

// WithField adds a field to the logger context
func (l *logger) WithField(key string, value interface{}) Interface {
	newLogger := &logger{
		config: l.config,
		fields: make(map[string]interface{}, len(l.fields)+1),
	}
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	newLogger.fields[key] = value
	return newLogger
}

// WithFields adds multiple fields to the logger context
func (l *logger) WithFields(fields map[string]interface{}) Interface {
	newLogger := &logger{
		config: l.config,
		fields: make(map[string]interface{}, len(l.fields)+len(fields)),
	}
	for k, v := range l.fields {
		newLogger.fields[k] = v
	}
	for k, v := range fields {
		newLogger.fields[k] = v
	}
	return newLogger
}

// WithError adds an error to the logger context
func (l *logger) WithError(err error) Interface {
	if err == nil {
		return l
	}
	return l.WithField("error", err.Error())
}

// Legacy methods for compatibility
func (l *logger) Infof(format string, args ...interface{}) {
	l.logf(LevelInfo, format, args...)
}

func (l *logger) Warningf(format string, args ...interface{}) {
	l.logf(LevelWarn, format, args...)
}

func (l *logger) Errorf(format string, args ...interface{}) {
	l.logf(LevelError, format, args...)
}

func (l *logger) Debugf(format string, args ...interface{}) {
	l.logf(LevelDebug, format, args...)
}

// Level-based methods
func (l *logger) Trace(msg string) {
	l.log(LevelTrace, msg)
}

func (l *logger) Debug(msg string) {
	l.log(LevelDebug, msg)
}

func (l *logger) Info(msg string) {
	l.log(LevelInfo, msg)
}

func (l *logger) Warn(msg string) {
	l.log(LevelWarn, msg)
}

func (l *logger) Error(msg string) {
	l.log(LevelError, msg)
}

func (l *logger) Fatal(msg string) {
	l.log(LevelFatal, msg)
	os.Exit(1)
}

func (l *logger) Tracef(format string, args ...interface{}) {
	l.logf(LevelTrace, format, args...)
}

// log handles the actual logging
func (l *logger) log(level Level, msg string) {
	if level < l.config.Level {
		return
	}

	switch l.config.Format {
	case FormatJSON:
		l.logJSON(level, msg)
	default:
		l.logText(level, msg)
	}

	if level == LevelFatal {
		os.Exit(1)
	}
}

// logf handles formatted logging
func (l *logger) logf(level Level, format string, args ...interface{}) {
	l.log(level, fmt.Sprintf(format, args...))
}

// logText outputs human-readable text
func (l *logger) logText(level Level, msg string) {
	timestamp := time.Now().Format("2006-01-02T15:04:05.000Z07:00")

	// Build the log entry
	var output string
	if l.config.Color && isTerminal() {
		levelStr := colorize(level.String(), levelColor(level))
		if l.config.Prefix != "" {
			output = fmt.Sprintf("%s [%s] %s: %s", timestamp, l.config.Prefix, levelStr, msg)
		} else {
			output = fmt.Sprintf("%s %s: %s", timestamp, levelStr, msg)
		}
	} else {
		if l.config.Prefix != "" {
			output = fmt.Sprintf("%s [%s] %s: %s", timestamp, l.config.Prefix, level.String(), msg)
		} else {
			output = fmt.Sprintf("%s %s: %s", timestamp, level.String(), msg)
		}
	}

	// Add fields if any
	if len(l.fields) > 0 {
		var fieldStrs []string
		for k, v := range l.fields {
			fieldStrs = append(fieldStrs, fmt.Sprintf("%s=%v", k, v))
		}
		output += " " + strings.Join(fieldStrs, " ")
	}

	// Add source location for debug and trace
	if level <= LevelDebug {
		if file, line := getSourceLocation(); file != "" {
			output += fmt.Sprintf(" [%s:%d]", file, line)
		}
	}

	if _, err := fmt.Fprintln(l.config.Output, output); err != nil {
		// If we can't write to the output, try to write to stderr as a fallback
		fmt.Fprintf(os.Stderr, "logger: failed to write log: %v\n", err)
	}
}

// logJSON outputs structured JSON
func (l *logger) logJSON(level Level, msg string) {
	entry := map[string]interface{}{
		"timestamp": time.Now().Format(time.RFC3339Nano),
		"level":     level.String(),
		"message":   msg,
	}

	if l.config.Prefix != "" {
		entry["prefix"] = l.config.Prefix
	}

	// Add fields
	for k, v := range l.fields {
		entry[k] = v
	}

	// Add source location for debug and trace
	if level <= LevelDebug {
		if file, line := getSourceLocation(); file != "" {
			entry["source"] = fmt.Sprintf("%s:%d", file, line)
		}
	}

	encoder := json.NewEncoder(l.config.Output)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(entry)
}

// Fatal logs an error and exits (package-level function for compatibility)
func Fatal(err error) {
	if err != nil {
		log.Printf("FATAL: %v", err)
	}
	os.Exit(1)
}

// Helper functions

// isTerminal checks if output is a terminal
func isTerminal() bool {
	if fileInfo, _ := os.Stderr.Stat(); (fileInfo.Mode() & os.ModeCharDevice) != 0 {
		return true
	}
	return false
}

// getSourceLocation returns the file and line number of the caller
func getSourceLocation() (string, int) {
	_, file, line, ok := runtime.Caller(4)
	if !ok {
		return "", 0
	}
	// Strip the path to just the filename
	parts := strings.Split(file, "/")
	return parts[len(parts)-1], line
}

// Color codes for different log levels
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorGray   = "\033[90m"
	colorWhite  = "\033[37m"
)

// levelColor returns the color for a log level
func levelColor(level Level) string {
	switch level {
	case LevelTrace:
		return colorGray
	case LevelDebug:
		return colorBlue
	case LevelInfo:
		return colorWhite
	case LevelWarn:
		return colorYellow
	case LevelError, LevelFatal:
		return colorRed
	default:
		return colorWhite
	}
}

// colorize applies color to text
func colorize(text, color string) string {
	return color + text + colorReset
}
