// Copyright (c) 2026, NVIDIA CORPORATION.  All rights reserved.
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

package profile

import (
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NVIDIA/k8s-test-infra/internal/capture/nvmldump"
)

// File names inside a capture node directory, as collect writes them.
const (
	dumpFile       = "nvml-dump.json.gz"
	dumpFilePlain  = "nvml-dump.json"
	smiXMLFile     = "smi-q.xml"
	dmiFile        = "host-dmi.txt"
	ibSysfsFile    = "host-ib-sysfs.txt"
	ibstatFile     = "host-ibstat.txt"
	pciNvidiaFile  = "host-pci-nvidia.txt"
	pciTreeFile    = "host-pci-hierarchy.txt"
	nvlinkSpeeds   = "smi-nvlink-s.txt"
	migProfiles    = "smi-mig-lgip.txt"
	migPlacements  = "smi-mig-lgipp.txt"
	notSupportedNA = "N/A"
)

// capture is one node directory of a committed, scrubbed capture.
type capture struct {
	name  string // <platform>/<node>, as the header names it
	dump  nvmldump.Recording
	gpus  []*xmlNode // the <gpu> elements of smi-q.xml, in order; nil without it
	files map[string]string
}

func loadCapture(dir string) (*capture, error) {
	c := &capture{name: filepath.Base(filepath.Dir(filepath.Clean(dir))) + "/" + filepath.Base(filepath.Clean(dir))}
	path := filepath.Join(dir, dumpFile)
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		path = filepath.Join(dir, dumpFilePlain)
	}
	d, err := nvmldump.Load(path)
	if err != nil {
		return nil, fmt.Errorf("reading the NVML dump: %w", err)
	}
	if len(d.Devices) == 0 {
		return nil, fmt.Errorf("%s records no devices", path)
	}
	for i, dev := range d.Devices {
		if dev.Index != i {
			return nil, fmt.Errorf("%s: device %d is recorded at position %d", path, dev.Index, i)
		}
	}
	c.dump = d
	if c.files, err = readFiles(dir); err != nil {
		return nil, err
	}
	if raw, ok := c.files[smiXMLFile]; ok {
		var root xmlNode
		if err := xml.Unmarshal([]byte(raw), &root); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", smiXMLFile, err)
		}
		c.gpus = root.all("gpu")
	}
	return c, nil
}

// readFiles reads the text files the rules use. Every file but the dump is
// optional: the rules that read one leave their keys out and say so in the
// profile.
func readFiles(dir string) (map[string]string, error) {
	files := map[string]string{}
	for _, name := range []string{smiXMLFile, dmiFile, ibSysfsFile, ibstatFile, pciNvidiaFile, pciTreeFile, nvlinkSpeeds, migProfiles, migPlacements} {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return nil, fmt.Errorf("reading %s: %w", name, err)
		}
		files[name] = string(raw)
	}
	return files, nil
}

func (c *capture) gpuCount() int { return len(c.dump.Devices) }

// call returns one recorded NVML answer of a GPU, or of the process when gpu
// is negative.
func (c *capture) call(gpu int, name string) (nvmldump.Call, bool) {
	calls := c.dump.System
	if gpu >= 0 {
		calls = c.dump.Devices[gpu].Calls
	}
	r, ok := calls[name]
	return r, ok
}

// at walks a decoded dump value: a string step selects a struct field, an int
// step a list element.
func at(v any, path ...any) (any, bool) {
	for _, step := range path {
		switch s := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				return nil, false
			}
			if v, ok = m[s]; !ok {
				return nil, false
			}
		case int:
			l, ok := v.([]any)
			if !ok || s >= len(l) {
				return nil, false
			}
			v = l[s]
		}
	}
	return v, true
}

func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

// cString decodes a NUL-terminated C char array, as the dump records them.
func cString(v any) (string, bool) {
	l, ok := v.([]any)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, x := range l {
		n, ok := asInt(x)
		if !ok {
			return "", false
		}
		if n == 0 {
			break
		}
		b.WriteByte(byte(n))
	}
	return b.String(), true
}

// xmlNode is a generic element of nvidia-smi -q -x.
type xmlNode struct {
	XMLName xml.Name
	Text    string     `xml:",chardata"`
	Kids    []*xmlNode `xml:",any"`
}

func (n *xmlNode) all(tag string) []*xmlNode {
	var out []*xmlNode
	for _, k := range n.Kids {
		if k.XMLName.Local == tag {
			out = append(out, k)
		}
	}
	return out
}

// find follows a slash-separated path, taking the first match at each step.
func (n *xmlNode) find(path string) *xmlNode {
	cur := n
	for _, tag := range strings.Split(path, "/") {
		kids := cur.all(tag)
		if len(kids) == 0 {
			return nil
		}
		cur = kids[0]
	}
	return cur
}

func (n *xmlNode) text() string { return strings.TrimSpace(n.Text) }
