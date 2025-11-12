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

// Package errors provides enhanced error handling for gpu-mockctl
package errors

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
)

// Error types for categorization
type ErrorType string

const (
	// ErrorTypeConfig indicates a configuration error
	ErrorTypeConfig ErrorType = "CONFIG"
	// ErrorTypeIO indicates an I/O error
	ErrorTypeIO ErrorType = "IO"
	// ErrorTypePermission indicates a permission error
	ErrorTypePermission ErrorType = "PERMISSION"
	// ErrorTypeValidation indicates a validation error
	ErrorTypeValidation ErrorType = "VALIDATION"
	// ErrorTypeSystem indicates a system error
	ErrorTypeSystem ErrorType = "SYSTEM"
	// ErrorTypeUnknown indicates an unknown error
	ErrorTypeUnknown ErrorType = "UNKNOWN"
)

// Error represents an enhanced error with context
type Error struct {
	Type    ErrorType
	Op      string   // Operation being performed
	Path    string   // File path if relevant
	Err     error    // Underlying error
	Context []string // Additional context
	Stack   string   // Stack trace for debugging
}

// Error implements the error interface
func (e *Error) Error() string {
	var parts []string

	if e.Type != "" {
		parts = append(parts, fmt.Sprintf("[%s]", e.Type))
	}

	if e.Op != "" {
		parts = append(parts, e.Op)
	}

	if e.Path != "" {
		parts = append(parts, fmt.Sprintf("path=%s", e.Path))
	}

	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}

	parts = append(parts, e.Context...)

	return strings.Join(parts, ": ")
}

// Unwrap returns the underlying error
func (e *Error) Unwrap() error {
	return e.Err
}

// WithStack adds stack trace to the error
func (e *Error) WithStack() *Error {
	e.Stack = getStackTrace()
	return e
}

// New creates a new error with context
func New(errType ErrorType, op string, err error) *Error {
	return &Error{
		Type: errType,
		Op:   op,
		Err:  err,
	}
}

// Wrap wraps an error with additional context
func Wrap(err error, op string) error {
	if err == nil {
		return nil
	}

	// If it's already our error type, add context
	if e, ok := err.(*Error); ok {
		e.Op = op + " > " + e.Op
		return e
	}

	// Determine error type from error
	errType := detectErrorType(err)

	return &Error{
		Type: errType,
		Op:   op,
		Err:  err,
	}
}

// WrapWithPath wraps an error with operation and path context
func WrapWithPath(err error, op, path string) error {
	if err == nil {
		return nil
	}

	e := &Error{
		Type: detectErrorType(err),
		Op:   op,
		Path: path,
		Err:  err,
	}

	return e
}

// Config creates a configuration error
func Config(op string, format string, args ...interface{}) error {
	return &Error{
		Type: ErrorTypeConfig,
		Op:   op,
		Err:  fmt.Errorf(format, args...),
	}
}

// IO creates an I/O error
func IO(op, path string, err error) error {
	return &Error{
		Type: ErrorTypeIO,
		Op:   op,
		Path: path,
		Err:  err,
	}
}

// Permission creates a permission error
func Permission(op, path string, err error) error {
	return &Error{
		Type: ErrorTypePermission,
		Op:   op,
		Path: path,
		Err:  err,
	}
}

// Validation creates a validation error
func Validation(op string, format string, args ...interface{}) error {
	return &Error{
		Type: ErrorTypeValidation,
		Op:   op,
		Err:  fmt.Errorf(format, args...),
	}
}

// IsType checks if an error is of a specific type
func IsType(err error, errType ErrorType) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Type == errType
	}
	return false
}

// GetType returns the error type
func GetType(err error) ErrorType {
	var e *Error
	if errors.As(err, &e) {
		return e.Type
	}
	return ErrorTypeUnknown
}

// detectErrorType attempts to detect the error type from the error
func detectErrorType(err error) ErrorType {
	if err == nil {
		return ErrorTypeUnknown
	}

	errStr := err.Error()
	lowerErr := strings.ToLower(errStr)

	// Check for permission errors
	if strings.Contains(lowerErr, "permission denied") ||
		strings.Contains(lowerErr, "access denied") ||
		strings.Contains(lowerErr, "operation not permitted") {
		return ErrorTypePermission
	}

	// Check for I/O errors
	if strings.Contains(lowerErr, "no such file") ||
		strings.Contains(lowerErr, "file exists") ||
		strings.Contains(lowerErr, "i/o") ||
		strings.Contains(lowerErr, "disk") {
		return ErrorTypeIO
	}

	// Check for validation errors
	if strings.Contains(lowerErr, "invalid") ||
		strings.Contains(lowerErr, "required") ||
		strings.Contains(lowerErr, "must") {
		return ErrorTypeValidation
	}

	return ErrorTypeUnknown
}

// getStackTrace returns a formatted stack trace
func getStackTrace() string {
	const depth = 32
	var pcs [depth]uintptr
	n := runtime.Callers(3, pcs[:])
	if n == 0 {
		return ""
	}

	frames := runtime.CallersFrames(pcs[:n])
	var sb strings.Builder

	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "runtime/") {
			sb.WriteString(fmt.Sprintf("%s:%d %s\n", frame.File, frame.Line, frame.Function))
		}
		if !more {
			break
		}
	}

	return sb.String()
}

// Recover recovers from panic and returns as error
func Recover(op string) error {
	if r := recover(); r != nil {
		err := fmt.Errorf("panic: %v", r)
		return &Error{
			Type:  ErrorTypeSystem,
			Op:    op,
			Err:   err,
			Stack: getStackTrace(),
		}
	}
	return nil
}
