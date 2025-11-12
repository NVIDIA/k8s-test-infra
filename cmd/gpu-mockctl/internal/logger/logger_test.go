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
	"bytes"
	"log"
	"os"
	"strings"
	"testing"
)

func TestLogger(t *testing.T) {
	// Capture log output
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	// Test non-verbose logger
	l := New("test", false)

	l.Infof("info message %d", 1)
	if !strings.Contains(buf.String(), "[test] INFO: info message 1") {
		t.Errorf("Expected info message, got: %s", buf.String())
	}
	buf.Reset()

	l.Warningf("warning message %s", "test")
	if !strings.Contains(buf.String(), "[test] WARNING: warning message test") {
		t.Errorf("Expected warning message, got: %s", buf.String())
	}
	buf.Reset()

	l.Errorf("error message")
	if !strings.Contains(buf.String(), "[test] ERROR: error message") {
		t.Errorf("Expected error message, got: %s", buf.String())
	}
	buf.Reset()

	// Debug should not print when verbose is false
	l.Debugf("debug message")
	if buf.String() != "" {
		t.Errorf("Expected no debug message when verbose=false, got: %s", buf.String())
	}

	// Test verbose logger
	vl := New("verbose-test", true)
	vl.Debugf("debug message visible")
	if !strings.Contains(buf.String(), "[verbose-test] DEBUG: debug message visible") {
		t.Errorf("Expected debug message when verbose=true, got: %s", buf.String())
	}
}
