// Copyright 2026 NVIDIA CORPORATION
// SPDX-License-Identifier: Apache-2.0

package seccompnotify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"slices"
	"sync"
	"time"
	"unsafe"

	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
	"golang.org/x/sys/unix"
)

// Request is one notified syscall.
type Request struct {
	// Container is the listener metadata of the container's seccomp policy.
	Container string
	// Pid is the caller in this process's PID namespace; 0 if not visible.
	Pid  uint32
	Args [6]uint64

	fd int
	id uint64
}

// The kernel's seccomp_data, seccomp_notif and seccomp_notif_resp, which
// x/sys/unix lacks. The ioctl numbers encode their sizes.
type seccompData struct {
	nr                 int32
	arch               uint32
	instructionPointer uint64
	args               [6]uint64
}

type seccompNotif struct {
	id    uint64
	pid   uint32
	flags uint32
	data  seccompData
}

type seccompNotifResp struct {
	id    uint64
	val   int64
	error int32
	flags uint32
}

// pollInterval bounds how long a stopped server keeps serving a container.
const pollInterval = time.Second

// handoverTimeout bounds one handover; runc writes it at once.
const handoverTimeout = 5 * time.Second

// maxFds bounds the fds of one handover; runc sends one.
const maxFds = 64

// stateLimit bounds the container state JSON sent with the fd.
const stateLimit = 1 << 20

// Run listens until ctx is cancelled, serving every container that connects.
func (s *Server) Run(ctx context.Context) error {
	listener, err := listen(s.path)
	if err != nil {
		return err
	}
	// Returning on an accept error stops the containers too, and closes the
	// listener so no further handover queues on a socket nobody reads.
	serveCtx, cancel := context.WithCancel(ctx)
	context.AfterFunc(serveCtx, func() {
		if err := listener.Close(); err != nil {
			zap.L().Warn("close seccomp listener", zap.Error(err))
		}
	})

	zap.L().Info("serving seccomp notifications", zap.String("listener", s.path))
	var (
		handling sync.Mutex
		served   sync.WaitGroup
	)
	defer served.Wait()
	defer cancel()
	for {
		conn, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", s.path, err)
		}
		// Handovers are serial: a stalled peer must not block the next.
		if err := conn.SetReadDeadline(time.Now().Add(handoverTimeout)); err != nil {
			zap.L().Debug("set handover deadline", zap.Error(err))
		}
		fd, state, err := receive(conn)
		if err := conn.Close(); err != nil {
			zap.L().Debug("close runc connection", zap.Error(err))
		}
		if errors.Is(err, io.EOF) {
			// The NRI plugin's liveness probe sends nothing.
			zap.L().Debug("listener probe")
			continue
		}
		if err != nil {
			zap.L().Warn("container not served: invalid seccomp handover", zap.Error(err))
			continue
		}
		zap.L().Info("serving container", zap.String("container", state.Metadata), zap.Int("pid", state.Pid))
		served.Go(func() { s.serve(serveCtx, fd, state.Metadata, &handling) })
	}
}

// listen replaces a socket left by an earlier instance.
func listen(path string) (*net.UnixListener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("remove stale listener %s: %w", path, err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	// Only runc, as root, should hand over a container's fd.
	if err := os.Chmod(path, 0o600); err != nil {
		if err := listener.Close(); err != nil {
			zap.L().Debug("close seccomp listener", zap.Error(err))
		}
		return nil, fmt.Errorf("restrict listener %s: %w", path, err)
	}
	return listener, nil
}

// receive reads runc's handover: the container state JSON and its seccomp fd.
func receive(conn *net.UnixConn) (int, specs.ContainerProcessState, error) {
	var state specs.ContainerProcessState

	buf := make([]byte, 64<<10)
	oob := make([]byte, unix.CmsgSpace(maxFds*4))
	n, oobn, flags, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return -1, state, fmt.Errorf("read: %w", err)
	}
	fds, err := rights(oob[:oobn])
	if err != nil {
		return -1, state, err
	}
	if flags&unix.MSG_CTRUNC != 0 {
		// Dropped fds would misalign state.Fds.
		closeAll(fds)
		return -1, state, fmt.Errorf("more than %d fds handed over", maxFds)
	}
	// The JSON may outlast the first read; runc then closes.
	rest, err := io.ReadAll(io.LimitReader(conn, stateLimit))
	if err != nil {
		closeAll(fds)
		return -1, state, fmt.Errorf("read: %w", err)
	}
	if err := json.Unmarshal(slices.Concat(buf[:n], rest), &state); err != nil {
		closeAll(fds)
		return -1, state, fmt.Errorf("decode container state: %w", err)
	}

	i := slices.Index(state.Fds, specs.SeccompFdName)
	if i < 0 || i >= len(fds) {
		closeAll(fds)
		return -1, state, fmt.Errorf("no %s among %v", specs.SeccompFdName, state.Fds)
	}
	fd := fds[i]
	closeAll(slices.Delete(fds, i, i+1))
	return fd, state, nil
}

func rights(oob []byte) ([]int, error) {
	messages, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("parse control message: %w", err)
	}
	var fds []int
	for i := range messages {
		received, err := unix.ParseUnixRights(&messages[i])
		if err != nil {
			closeAll(fds)
			return nil, fmt.Errorf("parse rights: %w", err)
		}
		fds = append(fds, received...)
	}
	return fds, nil
}

func closeAll(fds []int) {
	for _, fd := range fds {
		if err := unix.Close(fd); err != nil {
			zap.L().Debug("close received fd", zap.Error(err))
		}
	}
}

// serve answers the container's notifications until it exits or ctx ends.
func (s *Server) serve(ctx context.Context, fd int, container string, handling *sync.Mutex) {
	defer func() {
		if err := unix.Close(fd); err != nil {
			zap.L().Debug("close seccomp fd", zap.Error(err))
		}
	}()

	for ctx.Err() == nil {
		notif, ok, err := next(fd)
		if errors.Is(err, errExited) {
			zap.L().Info("container exited", zap.String("container", container))
			return
		}
		if err != nil {
			zap.L().Error("container no longer served", zap.String("container", container), zap.Error(err))
			return
		}
		if !ok {
			continue
		}

		handling.Lock()
		val, errno := s.handler(Request{
			Container: container,
			Pid:       notif.pid,
			Args:      notif.data.args,
			fd:        fd,
			id:        notif.id,
		})
		handling.Unlock()

		resp := seccompNotifResp{id: notif.id, val: val, error: -int32(errno)}
		if err := ioctl(fd, unix.SECCOMP_IOCTL_NOTIF_SEND, unsafe.Pointer(&resp)); err != nil {
			// ENOENT: the caller was killed while the handler ran.
			zap.L().Debug("send notification response", zap.String("container", container), zap.Error(err))
		}
	}
}

// errExited: every process under the container's filter has exited.
var errExited = errors.New("container exited")

// next waits up to pollInterval for a notification; ok is false if none came.
func next(fd int) (seccompNotif, bool, error) {
	var notif seccompNotif

	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds, int(pollInterval.Milliseconds()))
	if errors.Is(err, unix.EINTR) || (err == nil && n == 0) {
		return notif, false, nil
	}
	if err != nil {
		return notif, false, fmt.Errorf("poll seccomp fd: %w", err)
	}
	if fds[0].Revents&unix.POLLHUP != 0 {
		return notif, false, errExited
	}

	if err := ioctl(fd, unix.SECCOMP_IOCTL_NOTIF_RECV, unsafe.Pointer(&notif)); err != nil {
		// ENOENT: the caller was killed before its syscall was received.
		if errors.Is(err, unix.EINTR) || errors.Is(err, unix.ENOENT) {
			return notif, false, nil
		}
		return notif, false, fmt.Errorf("receive notification: %w", err)
	}
	return notif, true, nil
}

// String reads the NUL-terminated string at addr in the caller's memory, cut
// to limit bytes as the kernel cuts it. It fails once the caller has left the
// syscall, when its PID may name another process.
func (r Request) String(addr uint64, limit int) (string, error) {
	if r.Pid == 0 {
		return "", errors.New("caller is outside this PID namespace")
	}
	mem, err := os.Open(fmt.Sprintf("/proc/%d/mem", r.Pid))
	if err != nil {
		return "", fmt.Errorf("open caller memory: %w", err)
	}
	defer func() {
		if err := mem.Close(); err != nil {
			zap.L().Debug("close caller memory", zap.Error(err))
		}
	}()

	buf := make([]byte, limit+1)
	n, err := mem.ReadAt(buf, int64(addr)) //nolint:gosec // a user-space address fits in int64
	// A short read can end at an unmapped page past the string.
	if n == 0 && err != nil {
		return "", fmt.Errorf("read caller memory: %w", err)
	}
	s, err := cut(buf[:n], limit)
	if err != nil {
		return "", err
	}

	if err := ioctl(r.fd, unix.SECCOMP_IOCTL_NOTIF_ID_VALID, unsafe.Pointer(&r.id)); err != nil {
		return "", fmt.Errorf("caller left the syscall: %w", err)
	}
	return s, nil
}

// cut returns the string up to its NUL, or its first limit bytes.
func cut(read []byte, limit int) (string, error) {
	end := bytes.IndexByte(read, 0)
	switch {
	case end >= 0 && end <= limit:
	case len(read) > limit:
		end = limit
	default:
		// Memory ends first: the kernel faults there too.
		return "", fmt.Errorf("no string within %d readable bytes", len(read))
	}
	return string(read[:end]), nil
}

func ioctl(fd int, request uint, arg unsafe.Pointer) error {
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), uintptr(request), uintptr(arg)); errno != 0 {
		return errno
	}
	return nil
}
