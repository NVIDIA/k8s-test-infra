// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package daemon

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/NVIDIA/k8s-test-infra/pkg/network/mockib/protocol"
)

// Legacy ib_uverbs command IDs (linux/infiniband/uverbs.h).
const (
	ibUVCmdQueryDevice = 7
	ibUVCmdQueryPort   = 9
	ibUVCmdQueryGID    = 13
)

type verbsHandle struct {
	devName string
	caName  string
	readQ   [][]byte
	mu      sync.Mutex
}

func (s *Server) handleVerbsOpen(c net.Conn, req protocol.VerbsOpenReq) error {
	if req.DevName == "" {
		return protocol.WriteMessage(c, protocol.TypeVerbsOpen, protocol.VerbsOpenResp{Error: "dev_name required"})
	}
	idx, err := parseUverbsIndex(req.DevName)
	if err != nil {
		return protocol.WriteMessage(c, protocol.TypeVerbsOpen, protocol.VerbsOpenResp{Error: err.Error()})
	}
	caName := fmt.Sprintf("mlx5_%d", idx)
	var found bool
	for _, p := range s.localPorts {
		if p.CAName == caName {
			found = true
			break
		}
	}
	if !found {
		return protocol.WriteMessage(c, protocol.TypeVerbsOpen, protocol.VerbsOpenResp{
			Error: fmt.Sprintf("unknown device %s", req.DevName),
		})
	}
	s.verbsMu.Lock()
	id := s.nextVerbsHandleID + 1
	s.nextVerbsHandleID = id
	s.verbsHandles[id] = &verbsHandle{devName: req.DevName, caName: caName}
	s.verbsMu.Unlock()
	return protocol.WriteMessage(c, protocol.TypeVerbsOpen, protocol.VerbsOpenResp{Handle: id})
}

func (s *Server) handleVerbsWrite(c net.Conn, req protocol.VerbsWriteReq) error {
	h, ok := s.lookupVerbs(req.Handle)
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeVerbsWrite, protocol.VerbsWriteResp{Error: "invalid handle"})
	}
	resp, err := s.synthesizeVerbsWrite(h, req.Data)
	if err != nil {
		return protocol.WriteMessage(c, protocol.TypeVerbsWrite, protocol.VerbsWriteResp{Error: err.Error()})
	}
	if resp != nil {
		h.mu.Lock()
		h.readQ = append(h.readQ, resp)
		h.mu.Unlock()
	}
	return protocol.WriteMessage(c, protocol.TypeVerbsWrite, protocol.VerbsWriteResp{Written: len(req.Data)})
}

func (s *Server) handleVerbsRead(c net.Conn, req protocol.VerbsReadReq) error {
	h, ok := s.lookupVerbs(req.Handle)
	if !ok {
		return protocol.WriteMessage(c, protocol.TypeVerbsRead, protocol.VerbsReadResp{Error: "invalid handle"})
	}
	h.mu.Lock()
	var data []byte
	if len(h.readQ) > 0 {
		data = h.readQ[0]
		h.readQ = h.readQ[1:]
	}
	h.mu.Unlock()
	if req.MaxLen > 0 && len(data) > req.MaxLen {
		data = data[:req.MaxLen]
	}
	return protocol.WriteMessage(c, protocol.TypeVerbsRead, protocol.VerbsReadResp{Data: data})
}

func (s *Server) handleVerbsClose(c net.Conn, req protocol.VerbsCloseReq) error {
	s.verbsMu.Lock()
	delete(s.verbsHandles, req.Handle)
	s.verbsMu.Unlock()
	return protocol.WriteMessage(c, protocol.TypeVerbsClose, map[string]bool{"ok": true})
}

func (s *Server) lookupVerbs(id int) (*verbsHandle, bool) {
	s.verbsMu.RLock()
	defer s.verbsMu.RUnlock()
	h, ok := s.verbsHandles[id]
	return h, ok
}

func parseUverbsIndex(dev string) (int, error) {
	const pfx = "uverbs"
	if !strings.HasPrefix(dev, pfx) {
		return 0, fmt.Errorf("expected uverbsN, got %q", dev)
	}
	n, err := strconv.Atoi(dev[len(pfx):])
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid uverbs device %q", dev)
	}
	return n, nil
}

func (s *Server) synthesizeVerbsWrite(h *verbsHandle, data []byte) ([]byte, error) {
	if len(data) < 8 {
		return nil, fmt.Errorf("verbs cmd too short")
	}
	cmd := binary.LittleEndian.Uint32(data[0:4])
	// out_words is u16 at offset 4; response u64 at 8 — we return payload via read(2).
	switch cmd {
	case ibUVCmdQueryDevice:
		return s.buildQueryDeviceResp(h.caName)
	case ibUVCmdQueryPort:
		portNum := uint8(1)
		if len(data) >= 12 {
			portNum = data[12]
		}
		if portNum == 0 {
			portNum = 1
		}
		return s.buildQueryPortResp(h.caName, portNum)
	case ibUVCmdQueryGID:
		gidIdx := uint8(0)
		if len(data) >= 12 {
			gidIdx = data[12]
		}
		return s.buildQueryGIDResp(h.caName, gidIdx)
	default:
		return nil, nil
	}
}

func (s *Server) buildQueryDeviceResp(caName string) ([]byte, error) {
	// ib_uverbs_query_device_resp — smoke fields only; zero-fill rest.
	const respLen = 232
	buf := make([]byte, respLen)
	buf[131] = 1 // phys_port_cnt in ib_uverbs_query_device_resp (bookworm)
	nodeGUID, _ := s.readSysfsHex(caName, "node_guid")
	if nodeGUID != 0 {
		binary.LittleEndian.PutUint64(buf[8:16], nodeGUID)
		binary.LittleEndian.PutUint64(buf[16:24], nodeGUID)
	}
	return buf, nil
}

func (s *Server) buildQueryPortResp(caName string, portNum uint8) ([]byte, error) {
	const respLen = 40
	buf := make([]byte, respLen)
	// port state: 4 = ACTIVE
	buf[0] = 4
	lid, _ := s.readPortLID(caName)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(lid))
	// link_layer IB = 1
	buf[31] = 1
	_ = portNum
	return buf, nil
}

func (s *Server) buildQueryGIDResp(caName string, gidIdx uint8) ([]byte, error) {
	if gidIdx != 0 {
		return make([]byte, 16), nil
	}
	path := filepath.Join(s.cfg.IBRoot, "sys/class/infiniband", caName, "ports/1/gids/0")
	raw, err := os.ReadFile(path)
	if err != nil {
		return make([]byte, 16), nil
	}
	return gidBytesFromSysfs(string(raw)), nil
}

func gidBytesFromSysfs(s string) []byte {
	s = strings.TrimSpace(s)
	var out [16]byte
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		for i := 0; i < len(parts) && i < 8; i++ {
			var b uint64
			if _, err := fmt.Sscanf(parts[i], "%x", &b); err != nil {
				continue
			}
			out[i*2] = byte(b >> 8)
			out[i*2+1] = byte(b)
		}
		return out[:]
	}
	return out[:]
}

func (s *Server) readSysfsHex(caName, attr string) (uint64, error) {
	path := filepath.Join(s.cfg.IBRoot, "sys/class/infiniband", caName, attr)
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	sv := strings.NewReplacer(":", "", "\n", "").Replace(string(raw))
	return strconv.ParseUint(sv, 16, 64)
}

func (s *Server) readPortLID(caName string) (uint16, error) {
	path := filepath.Join(s.cfg.IBRoot, "sys/class/infiniband", caName, "ports/1/lid")
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	sv := strings.TrimSpace(string(raw))
	if strings.HasPrefix(sv, "0x") {
		v, err := strconv.ParseUint(sv, 2, 16)
		return uint16(v), err
	}
	v, err := strconv.ParseUint(sv, 10, 16)
	return uint16(v), err
}

// dispatchVerbs handles verbs RPC on the same Unix socket as UMAD.
func (s *Server) dispatchVerbs(c net.Conn, env protocol.Envelope) error {
	switch env.Type {
	case protocol.TypeVerbsOpen:
		var req protocol.VerbsOpenReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleVerbsOpen(c, req)
	case protocol.TypeVerbsWrite:
		var req protocol.VerbsWriteReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleVerbsWrite(c, req)
	case protocol.TypeVerbsRead:
		var req protocol.VerbsReadReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleVerbsRead(c, req)
	case protocol.TypeVerbsClose:
		var req protocol.VerbsCloseReq
		if err := protocol.DecodeBody(env, &req); err != nil {
			return err
		}
		return s.handleVerbsClose(c, req)
	default:
		return fmt.Errorf("unknown verbs message %q", env.Type)
	}
}
