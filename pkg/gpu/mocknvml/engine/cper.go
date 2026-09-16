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

package engine

import (
	"encoding/binary"
	"sync"
	"time"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// CPERAccessTypeGPU mirrors NVML_CPER_ACCESS_TYPE_GPU. It is the only record
// source NVML defines, so a query whose mask omits it matches nothing.
const CPERAccessTypeGPU uint32 = 1 << 0

// maxCPERRecords bounds the log. Real hardware keeps a finite RAS journal too;
// the oldest records age out first. Cursors carry a monotonic sequence rather
// than an index so truncation never makes a live cursor skip or repeat.
const maxCPERRecords = 128

// CPERQuery is the engine-side view of nvmlCPERCursor_v1_t.
type CPERQuery struct {
	TypeMask uint32
	// UUID filters to one GPU. Empty matches every GPU, which is what NVML
	// documents for an empty uuid string.
	UUID   string
	Handle uint64
}

// CPERResult is what the bridge copies back into nvmlGetCPER_v1_t.
type CPERResult struct {
	// Payload holds whole records only; a record is never split across calls.
	Payload []byte
	// NextHandle is the cursor the caller must pass on the following call.
	NextHandle uint64
	// Required is the buffer size the caller needs. It is set both for a size
	// query and when the supplied buffer could not hold the next record.
	Required uint32
}

// cperRecord is one encoded CPER record plus the metadata the cursor filters on.
type cperRecord struct {
	seq     uint64
	uuid    string
	encoded []byte
}

// cperFault identifies the injected fault a record was synthesised from, so a
// device that stays tripped does not accrue a new record on every query.
type cperFault struct {
	mode string
	xid  uint64
}

// cperLog is the node-level RAS journal. Records are appended when a device is
// observed newly tripped, sampled on the query path for the same reason the
// system event broker samples on the wait path: the mock runs no background
// thread.
type cperLog struct {
	mu       sync.Mutex
	records  []cperRecord
	nextSeq  uint64
	recorded map[int]cperFault

	// now is injectable so tests can assert on the encoded timestamp.
	now func() time.Time
}

func newCPERLog() *cperLog {
	return &cperLog{
		recorded: make(map[int]cperFault),
		now:      time.Now,
	}
}

// observe reconciles the log against the currently tripped faults. A device
// that has healed is forgotten, so re-injecting the same fault later records a
// genuinely new occurrence rather than being suppressed forever.
func (l *cperLog) observe(faults map[int]cperFaultSource) {
	l.mu.Lock()
	defer l.mu.Unlock()

	for index := range l.recorded {
		if _, still := faults[index]; !still {
			delete(l.recorded, index)
		}
	}

	for index, src := range faults {
		fault := cperFault{mode: src.Mode, xid: src.Xid}
		if prev, seen := l.recorded[index]; seen && prev == fault {
			continue
		}
		l.recorded[index] = fault
		l.appendLocked(src)
	}
}

func (l *cperLog) appendLocked(src cperFaultSource) {
	seq := l.nextSeq + 1
	l.nextSeq = seq
	l.records = append(l.records, cperRecord{
		seq:     seq,
		uuid:    src.UUID,
		encoded: encodeCPERRecord(src, seq, l.now()),
	})
	if len(l.records) > maxCPERRecords {
		l.records = l.records[len(l.records)-maxCPERRecords:]
	}
}

// pending returns the records a cursor has not yet consumed, oldest first.
func (l *cperLog) pending(q CPERQuery) []cperRecord {
	l.mu.Lock()
	defer l.mu.Unlock()

	var out []cperRecord
	for _, rec := range l.records {
		if rec.seq < q.Handle {
			continue
		}
		if q.UUID != "" && rec.uuid != q.UUID {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// cperFaultSource is everything a record needs about the device that faulted.
type cperFaultSource struct {
	UUID  string
	GpuID uint32
	Mode  string
	Xid   uint64
}

// SystemGetCPER answers nvmlSystemGetCPER_v1. sizeQuery selects the
// buffer==NULL && bufferSize==0 form, which reports the space needed without
// advancing the cursor.
//
// Returns match the NVML contract: SUCCESS with an empty payload and
// Required==0 when there are no (more) records, INSUFFICIENT_SIZE with
// Required set when records exist but do not fit, otherwise SUCCESS with the
// records and an advanced cursor.
func (e *Engine) SystemGetCPER(q CPERQuery, bufferSize uint32, sizeQuery bool) (CPERResult, nvml.Return) {
	// A mask that does not ask for GPU records matches nothing. NVML defines no
	// other source, so this is "no records" rather than an invalid argument.
	if q.TypeMask&CPERAccessTypeGPU == 0 {
		return CPERResult{NextHandle: q.Handle}, nvml.SUCCESS
	}

	e.cper.observe(e.cperFaults())

	pending := e.cper.pending(q)
	if len(pending) == 0 {
		return CPERResult{NextHandle: q.Handle}, nvml.SUCCESS
	}

	var total uint32
	for _, rec := range pending {
		total += uint32(len(rec.encoded))
	}
	if sizeQuery {
		return CPERResult{NextHandle: q.Handle, Required: total}, nvml.ERROR_INSUFFICIENT_SIZE
	}

	// Pack whole records only. A buffer too small for even the first record is
	// INSUFFICIENT_SIZE with that record's size, so the caller can grow and
	// retry with the same cursor.
	payload := make([]byte, 0, min(total, bufferSize))
	next := q.Handle
	for _, rec := range pending {
		if uint32(len(payload)+len(rec.encoded)) > bufferSize {
			break
		}
		payload = append(payload, rec.encoded...)
		next = rec.seq + 1
	}
	if len(payload) == 0 {
		return CPERResult{NextHandle: q.Handle, Required: uint32(len(pending[0].encoded))},
			nvml.ERROR_INSUFFICIENT_SIZE
	}
	return CPERResult{Payload: payload, NextHandle: next}, nvml.SUCCESS
}

// cperFaults collects the currently tripped faults across visible devices.
// Only the modes that correspond to a hardware error produce a record: a
// healthy device, and one whose injector has not tripped yet, contribute none.
func (e *Engine) cperFaults() map[int]cperFaultSource {
	e.mu.RLock()
	defer e.mu.RUnlock()

	faults := make(map[int]cperFaultSource)
	if e.initCount == 0 || e.server == nil {
		return faults
	}

	for index, dev := range e.server.configurableDevices {
		if dev == nil || !e.server.isDeviceVisible(index) {
			continue
		}
		fi := dev.failureInjector()
		// FaultByConfig also covers the device the config declares immediately
		// failed, which a client that only reads the RAS log would otherwise
		// never see trip. It never rolls probability or advances after_calls.
		if !fi.FaultByConfig() {
			continue
		}
		faults[index] = cperFaultSource{
			UUID:  dev.UUID,
			GpuID: dev.boardID,
			Mode:  fi.Mode(),
			// ConfiguredXid peeks without consuming the event-set delivery, so
			// a CPER read never steals the Xid from nvmlEventSetWait.
			Xid: fi.ConfiguredXid(),
		}
	}
	return faults
}

// UEFI CPER layout constants (UEFI 2.10, Appendix N).
const (
	cperHeaderSize  = 128
	cperSectionSize = 72

	cperRecordRevision  = 0x0100
	cperSectionRevision = 0x0100
	cperSignatureEnd    = 0xFFFFFFFF

	// Severity encoding from the UEFI Error Severity table.
	cperSeverityFatal = 1

	// Record header validation bits: timestamp is the only field the mock
	// fills in, so platform and partition id stay marked invalid rather than
	// presenting a plausible all-zeros GUID.
	cperValidTimestamp = 1 << 1

	// Hardware Error Record flags. The mock always sets SIMULATED: a consumer
	// that honours the flag can tell mock telemetry from a real fault, which
	// is exactly the distinction a simulator should not hide.
	cperFlagSimulated = 1 << 2

	// Section descriptor: the single section is the primary one, and the mock
	// supplies neither a FRU id nor a FRU string.
	cperSectionFlagPrimary = 1 << 0
)

// Section body sizes.
const (
	// cperMemorySectionSize is the UEFI Memory Error Record (Appendix N,
	// "Memory Error Section").
	cperMemorySectionSize = 73
	// cperGPUSectionSize is the mock's own vendor section, laid out below.
	cperGPUSectionSize = 64
)

// UEFI "Memory Error Section" type GUID. An uncorrectable DRAM ECC error is
// genuinely a memory error, so those records use the standardised container and
// a consumer with a UEFI CPER parser decodes them for real.
var cperMemorySectionGUID = cperGUID{0xA5BC1114, 0x6F64, 0x4EDE, [8]byte{0xB8, 0x63, 0x3E, 0x83, 0xED, 0x7C, 0x83, 0xB1}}

// GUIDs the mock owns. NVIDIA does not publish the creator, notification or
// section GUIDs its driver stamps into GPU CPER records, so the mock must not
// impersonate them. The first word spells "MOCK" (0x4D4F434B), the same marker
// the mock's device UUIDs carry. A consumer that walks the record header and
// skips unrecognised section types behaves identically against the mock and
// against hardware; one that special-cases NVIDIA's GUID is not tricked into
// reading a synthetic body as real telemetry.
var (
	cperCreatorGUID      = cperGUID{0x4D4F434B, 0, 0, [8]byte{0, 0, 0, 0, 0, 0, 0, 1}}
	cperNotificationGUID = cperGUID{0x4D4F434B, 0, 0, [8]byte{0, 0, 0, 0, 0, 0, 0, 2}}
	cperGPUSectionGUID   = cperGUID{0x4D4F434B, 0, 0, [8]byte{0, 0, 0, 0, 0, 0, 0, 3}}
)

// cperGUID is a UEFI GUID. CPER stores the first three fields little-endian and
// the last eight bytes in order, so it needs its own marshalling.
type cperGUID struct {
	data1 uint32
	data2 uint16
	data3 uint16
	data4 [8]byte
}

func (g cperGUID) encode(dst []byte) {
	binary.LittleEndian.PutUint32(dst[0:4], g.data1)
	binary.LittleEndian.PutUint16(dst[4:6], g.data2)
	binary.LittleEndian.PutUint16(dst[6:8], g.data3)
	copy(dst[8:16], g.data4[:])
}

// encodeCPERRecord builds a complete single-section CPER record: the 128-byte
// record header, one 72-byte section descriptor, and the section body.
func encodeCPERRecord(src cperFaultSource, recordID uint64, at time.Time) []byte {
	body, sectionGUID := encodeCPERSectionBody(src)
	total := cperHeaderSize + cperSectionSize + len(body)
	out := make([]byte, total)

	header := out[:cperHeaderSize]
	copy(header[0:4], "CPER")
	binary.LittleEndian.PutUint16(header[4:6], cperRecordRevision)
	binary.LittleEndian.PutUint32(header[6:10], cperSignatureEnd)
	binary.LittleEndian.PutUint16(header[10:12], 1) // one section
	binary.LittleEndian.PutUint32(header[12:16], cperSeverityFatal)
	binary.LittleEndian.PutUint32(header[16:20], cperValidTimestamp)
	binary.LittleEndian.PutUint32(header[20:24], uint32(total))
	encodeCPERTimestamp(header[24:32], at)
	// header[32:48] platform id and header[48:64] partition id stay zero;
	// their validation bits are clear.
	cperCreatorGUID.encode(header[64:80])
	cperNotificationGUID.encode(header[80:96])
	binary.LittleEndian.PutUint64(header[96:104], recordID)
	binary.LittleEndian.PutUint32(header[104:108], cperFlagSimulated)
	// header[108:116] persistence info and header[116:128] reserved stay zero.

	section := out[cperHeaderSize : cperHeaderSize+cperSectionSize]
	binary.LittleEndian.PutUint32(section[0:4], cperHeaderSize+cperSectionSize)
	binary.LittleEndian.PutUint32(section[4:8], uint32(len(body)))
	binary.LittleEndian.PutUint16(section[8:10], cperSectionRevision)
	// section[10] validation bits stay zero: no FRU id, no FRU string.
	binary.LittleEndian.PutUint32(section[12:16], cperSectionFlagPrimary)
	sectionGUID.encode(section[16:32])
	// section[32:48] FRU id stays zero.
	binary.LittleEndian.PutUint32(section[48:52], cperSeverityFatal)
	// section[52:72] FRU string stays zero.

	copy(out[cperHeaderSize+cperSectionSize:], body)
	return out
}

// encodeCPERSectionBody picks the section that actually describes the fault:
// the standard UEFI memory error record for uncorrectable ECC, the mock's own
// GPU section for a GPU that fell off the bus.
func encodeCPERSectionBody(src cperFaultSource) (body []byte, sectionGUID cperGUID) {
	if src.Mode == FailureModeECCUncorrectable {
		return encodeCPERMemorySection(), cperMemorySectionGUID
	}
	return encodeCPERGPUSection(src), cperGPUSectionGUID
}

// Fields of the UEFI Memory Error Record the mock fills in. The validation
// bitmask follows the UEFI Memory Error Record table; the error type follows
// its Memory Error Type table.
const (
	cperMemoryValidErrorStatus     = 1 << 0
	cperMemoryValidErrorType       = 1 << 14
	cperMemoryErrorTypeMultiBitECC = 3
	// cperMemoryUncorrectedError is bit 2 of the UEFI Error Status field.
	cperMemoryUncorrectedError = 1 << 2
	// cperMemoryErrorTypeOffset is the last byte of the record.
	cperMemoryErrorTypeOffset = cperMemorySectionSize - 1
)

func encodeCPERMemorySection() []byte {
	body := make([]byte, cperMemorySectionSize)
	// Only the fields the mock can honestly fill are marked valid; the physical
	// address, bank, row and column of a simulated error would be fiction, and
	// a parser that trusts the validation bits must not read them.
	binary.LittleEndian.PutUint64(body[0:8], cperMemoryValidErrorStatus|cperMemoryValidErrorType)
	binary.LittleEndian.PutUint64(body[8:16], cperMemoryUncorrectedError)
	body[cperMemoryErrorTypeOffset] = cperMemoryErrorTypeMultiBitECC
	return body
}

// encodeCPERGPUSection lays out the mock's vendor section:
//
//	offset size field
//	0      8    Xid code, 0 when the fault carries none
//	8      4    PCI-format GPU id (matches nvmlSystemEventData_v1_t.gpuId)
//	12     4    failure mode, 1 lost / 2 fallen_off_bus / 3 ecc_uncorrectable
//	16     48   GPU UUID, NUL padded
func encodeCPERGPUSection(src cperFaultSource) []byte {
	body := make([]byte, cperGPUSectionSize)
	binary.LittleEndian.PutUint64(body[0:8], src.Xid)
	binary.LittleEndian.PutUint32(body[8:12], src.GpuID)
	binary.LittleEndian.PutUint32(body[12:16], cperFailureModeCode(src.Mode))
	uuid := src.UUID
	if len(uuid) > len(body)-16-1 {
		uuid = uuid[:len(body)-16-1]
	}
	copy(body[16:], uuid)
	return body
}

func cperFailureModeCode(mode string) uint32 {
	switch mode {
	case FailureModeLost:
		return 1
	case FailureModeFallenOffBus:
		return 2
	case FailureModeECCUncorrectable:
		return 3
	default:
		return 0
	}
}

// encodeCPERTimestamp writes the UEFI BCD timestamp: seconds, minutes, hours,
// a flag byte whose bit 0 marks the time as precise, then day, month, year and
// century. The mock always claims precision — it knows exactly when it
// synthesised the record.
func encodeCPERTimestamp(dst []byte, at time.Time) {
	at = at.UTC()
	dst[0] = toBCD(at.Second())
	dst[1] = toBCD(at.Minute())
	dst[2] = toBCD(at.Hour())
	dst[3] = 1
	dst[4] = toBCD(at.Day())
	dst[5] = toBCD(int(at.Month()))
	dst[6] = toBCD(at.Year() % 100)
	dst[7] = toBCD(at.Year() / 100)
}

func toBCD(v int) byte {
	return byte((v/10)<<4 | v%10)
}
