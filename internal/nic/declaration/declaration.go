// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: Copyright 2026 NVIDIA CORPORATION

// Package declaration checks NIC declarations (MEP-0016): SNICProfiles, the
// NIC slots that place them on a Node and the rack-group overrides of those
// slots. File mode and the control plane share it, so a declaration means the
// same in both.
//
// The CRDs check field shapes when an object reaches the API server. File mode
// never goes through one, so the shape checks are repeated here next to the
// rules that span fields.
package declaration

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/NVIDIA/k8s-test-infra/api/v1alpha1"
)

const (
	kindNICProfile  = "SNICProfile"
	kindRackProfile = "SGPURackProfile"
)

var (
	hexID       = regexp.MustCompile(`^[0-9a-f]{4}$`)
	hexValue    = regexp.MustCompile(`^0x[0-9a-fA-F]+$`)
	lowerHex    = regexp.MustCompile(`^0x[0-9a-f]+$`)
	classCode   = regexp.MustCompile(`^0x[0-9a-f]{6}$`)
	revision    = regexp.MustCompile(`^0x[0-9a-f]{2}$`)
	capMask     = regexp.MustCompile(`^0x[0-9a-f]{8}$`)
	bdf         = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-7]$`)
	deviceAddr  = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{2}:[0-9a-f]{2}$`)
	rootComplex = regexp.MustCompile(`^pci[0-9a-f]{4}:[0-9a-f]{2}$`)
	umadMode    = regexp.MustCompile(`^[0-7]{3,4}$`)
)

// laneGbps is the per-lane rate of each lane speed, as the kernel reports
// port rates.
var laneGbps = map[v1alpha1.LaneSpeed]float64{
	"SDR":   2.5,
	"DDR":   5,
	"QDR":   10,
	"FDR10": 10,
	"FDR":   14,
	"EDR":   25,
	"HDR":   50,
	"NDR":   100,
	"XDR":   200,
}

// LaneGbps returns the per-lane rate of a lane speed in Gb/s.
func LaneGbps(s v1alpha1.LaneSpeed) (float64, bool) {
	g, ok := laneGbps[s]
	return g, ok
}

// Profiles are SNICProfiles by name.
type Profiles map[string]*v1alpha1.SNICProfile

// DecodeProfile strictly decodes one SNICProfile document and validates it.
// An unknown field is an error, so a typo can't silently drop a value.
func DecodeProfile(data []byte) (*v1alpha1.SNICProfile, error) {
	var p v1alpha1.SNICProfile
	if err := decode(data, kindNICProfile, &p.TypeMeta, &p, ""); err != nil {
		return nil, err
	}
	if p.Name == "" {
		return nil, errors.New("SNICProfile metadata.name is empty")
	}
	if err := ValidateProfile(&p); err != nil {
		return nil, fmt.Errorf("SNICProfile %s: %w", p.Name, err)
	}
	return &p, nil
}

// rackNICPaths are the parts of a rack profile NIC declaration reads. Only
// they are checked raw, so a fragment holding just them decodes.
var rackNICPaths = []string{
	"spec.node.topology.rootPorts", "spec.node.topology.gpuSlots", "spec.node.topology.nics", "spec.software.rdma",
}

// DecodeRackProfile strictly decodes one SGPURackProfile document. It doesn't
// validate: ValidateNode does, against the NIC profiles the slots name.
func DecodeRackProfile(data []byte) (*v1alpha1.SGPURackProfile, error) {
	var r v1alpha1.SGPURackProfile
	if err := decode(data, kindRackProfile, &r.TypeMeta, &r, rackNICPaths...); err != nil {
		return nil, err
	}
	return &r, nil
}

// decode reads a document of kind strictly: a repeated key, an unknown field,
// a missing required key or a repeated item of a set or map list is an error,
// as the API server would make it. paths are the parts checked raw, "" for
// the whole document.
func decode(data []byte, kind string, meta *metav1.TypeMeta, into any, paths ...string) error {
	// An unknown field first: a misspelled key also leaves its field missing,
	// and the misspelling is the useful message.
	if err := yaml.UnmarshalStrict(data, into); err != nil {
		return fmt.Errorf("decode %s: %w", kind, err)
	}
	if meta.Kind != kind || meta.APIVersion != v1alpha1.GroupVersion.String() {
		return fmt.Errorf("got %s %s, want %s %s", meta.APIVersion, meta.Kind, v1alpha1.GroupVersion, kind)
	}
	if err := DecodeRaw(data, kind, paths...); err != nil {
		return fmt.Errorf("decode %s: %w", kind, err)
	}
	return nil
}

// problems collects everything wrong with a declaration, so one pass reports
// all of it rather than the first mistake.
type problems struct{ errs []error }

func (p *problems) check(ok bool, format string, args ...any) {
	if !ok {
		p.errs = append(p.errs, fmt.Errorf(format, args...))
	}
}

func (p *problems) add(err error) {
	if err != nil {
		p.errs = append(p.errs, err)
	}
}

func (p *problems) err() error { return errors.Join(p.errs...) }

func checkPCIID(pr *problems, field string, id v1alpha1.PCIID) {
	pr.check(hexID.MatchString(id.VendorID), "%s vendorID %q is not four lowercase hex digits", field, id.VendorID)
	pr.check(hexID.MatchString(id.DeviceID), "%s deviceID %q is not four lowercase hex digits", field, id.DeviceID)
}

// optional reports whether v is empty or matches re.
func optional(v string, re *regexp.Regexp) bool { return v == "" || re.MatchString(v) }

// DecodeRaw checks a YAML document, or the parts of it at paths, against the
// CRD schema of kind before it is decoded into Go's types; see checkRaw. A
// repeated key is an error too.
func DecodeRaw(data []byte, kind string, paths ...string) error {
	j, err := yaml.YAMLToJSONStrict(data)
	if err != nil {
		return err
	}
	var raw any
	if err := json.Unmarshal(j, &raw); err != nil {
		return err
	}
	var pr problems
	for _, path := range paths {
		if path == "" {
			checkRaw(&pr, raw, schemas[kind], "")
			continue
		}
		checkRawAt(&pr, raw, kind, path)
	}
	return pr.err()
}
