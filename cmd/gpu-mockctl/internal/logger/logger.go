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
	"fmt"
	"log"
	"os"
)

// Interface defines logging methods matching toolkit patterns
type Interface interface {
	Infof(format string, args ...interface{})
	Warningf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Debugf(format string, args ...interface{})
}

// logger implements Interface
type logger struct {
	prefix  string
	verbose bool
}

// New creates a new logger instance
func New(prefix string, verbose bool) Interface {
	return &logger{
		prefix:  prefix,
		verbose: verbose,
	}
}

func (l *logger) Infof(format string, args ...interface{}) {
	log.Printf("[%s] INFO: %s", l.prefix, fmt.Sprintf(format, args...))
}

func (l *logger) Warningf(format string, args ...interface{}) {
	log.Printf("[%s] WARNING: %s", l.prefix, fmt.Sprintf(format, args...))
}

func (l *logger) Errorf(format string, args ...interface{}) {
	log.Printf("[%s] ERROR: %s", l.prefix, fmt.Sprintf(format, args...))
}

func (l *logger) Debugf(format string, args ...interface{}) {
	if l.verbose {
		log.Printf("[%s] DEBUG: %s", l.prefix, fmt.Sprintf(format, args...))
	}
}

// Fatal logs an error and exits
func Fatal(err error) {
	if err != nil {
		log.Fatalf("FATAL: %v", err)
	}
	os.Exit(1)
}
