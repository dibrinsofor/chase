//go:build linux && experimental_ebpf

package tracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall" -target amd64,arm64 trace ./bpf/trace.c

// eBPF-specific types that extend the base tracer types.
// These are only available when building with experimental_ebpf tag.

// EBPFFileAccess extends FileAccess with eBPF-specific fields.
type EBPFFileAccess struct {
	FileAccess
	PID       int
	TID       int
	Timestamp uint64
	Flags     uint32
}

// ProcessInfo captures subprocess spawn information from execve.
type ProcessInfo struct {
	PID       int
	PPID      int
	Comm      string
	Filename  string
	Argv      []string
	Timestamp uint64
}

// EBPFConfig extends Config with eBPF-specific options.
type EBPFConfig struct {
	Config
	FilterPID      int
	FollowChildren bool
}

const maxPathLen = 256
const maxArgvLen = 256

type bpfEvent struct {
	Pid       uint32
	Tid       uint32
	Timestamp uint64
	Flags     uint32
	Op        uint8
	EventType uint8
	_         [2]uint8
	Path      [maxPathLen]byte
}

type bpfExecEvent struct {
	Pid       uint32
	Ppid      uint32
	Timestamp uint64
	EventType uint8
	_         [3]uint8
	Comm      [16]byte
	Filename  [maxPathLen]byte
	Argv      [maxArgvLen]byte
}

// ebpfTracerLinux implements the eBPF-based tracer for Linux.
// This provides more detailed subprocess tracking than fsatrace.
type ebpfTracerLinux struct {
	cfg            EBPFConfig
	objs           *traceObjects
	links          []link.Link
	reader         *ringbuf.Reader
	execReader     *ringbuf.Reader
	events         chan EBPFFileAccess
	collected      []EBPFFileAccess
	collectedProcs []ProcessInfo
	mu             sync.Mutex
	running        bool
	done           chan struct{}
	execDone       chan struct{}
}

// NewEBPFTracer creates an eBPF-based tracer (Linux only, requires experimental_ebpf tag).
func NewEBPFTracer(cfg EBPFConfig) (*ebpfTracerLinux, error) {
	return &ebpfTracerLinux{
		cfg:      cfg,
		events:   make(chan EBPFFileAccess, 1024),
		done:     make(chan struct{}),
		execDone: make(chan struct{}),
	}, nil
}

// newPlatformTracer returns the eBPF tracer when built with experimental_ebpf tag.
// Note: This implements the simpler Tracer interface by wrapping eBPF functionality.
func newPlatformTracer(cfg Config) (Tracer, error) {
	return &ebpfWrapperTracer{
		cfg: EBPFConfig{
			Config:         cfg,
			FilterPID:      0,
			FollowChildren: true,
		},
	}, nil
}

// ebpfWrapperTracer wraps the eBPF tracer to implement the simpler Tracer interface.
type ebpfWrapperTracer struct {
	cfg      EBPFConfig
	inner    *ebpfTracerLinux
	ctx      context.Context
	cancel   context.CancelFunc
	cmd      *exec.Cmd
	accesses []FileAccess
}

func (t *ebpfWrapperTracer) WrapCommand(shell []string, cmdStr string) (*exec.Cmd, error) {
	// For eBPF, we don't wrap the command - we trace it directly
	args := append(shell[1:], cmdStr)
	t.cmd = exec.Command(shell[0], args...)
	return t.cmd, nil
}

func (t *ebpfWrapperTracer) ParseOutput() ([]FileAccess, error) {
	return t.accesses, nil
}

func (t *ebpfWrapperTracer) Cleanup() error {
	if t.cancel != nil {
		t.cancel()
	}
	return nil
}

func (t *ebpfTracerLinux) Start(ctx context.Context, pid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return errors.New("tracer already running")
	}

	spec, err := loadTrace()
	if err != nil {
		return fmt.Errorf("load trace spec: %w", err)
	}

	if err := spec.RewriteConstants(map[string]interface{}{
		"filter_pid":      uint32(pid),
		"follow_children": t.cfg.FollowChildren,
	}); err != nil {
		return fmt.Errorf("rewrite constants: %w", err)
	}

	t.objs = &traceObjects{}
	if err := spec.LoadAndAssign(t.objs, nil); err != nil {
		return fmt.Errorf("load and assign: %w", err)
	}

	openatLink, err := link.Tracepoint("syscalls", "sys_enter_openat", t.objs.TraceOpenatEnter, nil)
	if err != nil {
		t.cleanup()
		return fmt.Errorf("attach openat tracepoint: %w", err)
	}
	t.links = append(t.links, openatLink)

	if t.cfg.FollowChildren {
		forkLink, err := link.Tracepoint("sched", "sched_process_fork", t.objs.TraceFork, nil)
		if err != nil {
			t.cleanup()
			return fmt.Errorf("attach fork tracepoint: %w", err)
		}
		t.links = append(t.links, forkLink)

		exitLink, err := link.Tracepoint("sched", "sched_process_exit", t.objs.TraceExit, nil)
		if err != nil {
			t.cleanup()
			return fmt.Errorf("attach exit tracepoint: %w", err)
		}
		t.links = append(t.links, exitLink)
	}

	t.reader, err = ringbuf.NewReader(t.objs.Events)
	if err != nil {
		t.cleanup()
		return fmt.Errorf("create ring buffer reader: %w", err)
	}

	t.execReader, err = ringbuf.NewReader(t.objs.ExecEvents)
	if err != nil {
		t.cleanup()
		return fmt.Errorf("create exec ring buffer reader: %w", err)
	}

	// Attach execve tracepoint for subprocess tracking
	execveLink, err := link.Tracepoint("syscalls", "sys_enter_execve", t.objs.TraceExecve, nil)
	if err != nil {
		t.cleanup()
		return fmt.Errorf("attach execve tracepoint: %w", err)
	}
	t.links = append(t.links, execveLink)

	if pid > 0 {
		if err := t.objs.TargetPids.Put(uint32(pid), uint32(1)); err != nil {
			t.cleanup()
			return fmt.Errorf("add target pid: %w", err)
		}
	}

	t.running = true
	t.done = make(chan struct{})
	t.execDone = make(chan struct{})

	go t.readEvents(ctx)
	go t.readExecEvents(ctx)

	return nil
}

func (t *ebpfTracerLinux) readEvents(ctx context.Context) {
	defer close(t.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		record, err := t.reader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			continue
		}

		var event bpfEvent
		if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &event); err != nil {
			continue
		}

		path := unix.ByteSliceToString(event.Path[:])
		if path == "" {
			continue
		}

		fa := EBPFFileAccess{
			FileAccess: FileAccess{
				Path:      path,
				Operation: flagsToOp(event.Flags),
			},
			PID:       int(event.Pid),
			TID:       int(event.Tid),
			Timestamp: event.Timestamp,
			Flags:     event.Flags,
		}

		t.mu.Lock()
		t.collected = append(t.collected, fa)
		t.mu.Unlock()

		select {
		case t.events <- fa:
		default:
		}
	}
}

func (t *ebpfTracerLinux) readExecEvents(ctx context.Context) {
	defer close(t.execDone)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		record, err := t.execReader.Read()
		if err != nil {
			if errors.Is(err, ringbuf.ErrClosed) {
				return
			}
			continue
		}

		var event bpfExecEvent
		if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &event); err != nil {
			continue
		}

		comm := unix.ByteSliceToString(event.Comm[:])
		filename := unix.ByteSliceToString(event.Filename[:])
		argv := unix.ByteSliceToString(event.Argv[:])

		if filename == "" {
			continue
		}

		proc := ProcessInfo{
			PID:       int(event.Pid),
			PPID:      int(event.Ppid),
			Comm:      comm,
			Filename:  filename,
			Argv:      []string{argv},
			Timestamp: event.Timestamp,
		}

		t.mu.Lock()
		t.collectedProcs = append(t.collectedProcs, proc)
		t.mu.Unlock()
	}
}

func flagsToOp(flags uint32) Operation {
	const (
		O_RDONLY = 0x0
		O_WRONLY = 0x1
		O_RDWR   = 0x2
	)

	accessMode := flags & 0x3
	switch accessMode {
	case O_WRONLY, O_RDWR:
		return OpWrite
	default:
		return OpRead
	}
}

func (t *ebpfTracerLinux) Stop() ([]EBPFFileAccess, []ProcessInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return t.collected, t.collectedProcs, nil
	}

	if t.reader != nil {
		t.reader.Close()
	}
	if t.execReader != nil {
		t.execReader.Close()
	}

	<-t.done
	<-t.execDone

	t.cleanup()
	t.running = false

	return t.collected, t.collectedProcs, nil
}

func (t *ebpfTracerLinux) cleanup() {
	for _, l := range t.links {
		l.Close()
	}
	t.links = nil

	if t.objs != nil {
		t.objs.Close()
		t.objs = nil
	}
}

func (t *ebpfTracerLinux) Events() <-chan EBPFFileAccess {
	return t.events
}

type traceObjects struct {
	TraceOpenatEnter *ebpf.Program `ebpf:"trace_openat_enter"`
	TraceFork        *ebpf.Program `ebpf:"trace_fork"`
	TraceExit        *ebpf.Program `ebpf:"trace_exit"`
	TraceExecve      *ebpf.Program `ebpf:"trace_execve"`
	Events           *ebpf.Map     `ebpf:"events"`
	ExecEvents       *ebpf.Map     `ebpf:"exec_events"`
	TargetPids       *ebpf.Map     `ebpf:"target_pids"`
}

func (o *traceObjects) Close() error {
	if o.TraceOpenatEnter != nil {
		o.TraceOpenatEnter.Close()
	}
	if o.TraceFork != nil {
		o.TraceFork.Close()
	}
	if o.TraceExit != nil {
		o.TraceExit.Close()
	}
	if o.TraceExecve != nil {
		o.TraceExecve.Close()
	}
	if o.Events != nil {
		o.Events.Close()
	}
	if o.ExecEvents != nil {
		o.ExecEvents.Close()
	}
	if o.TargetPids != nil {
		o.TargetPids.Close()
	}
	return nil
}

func loadTrace() (*ebpf.CollectionSpec, error) {
	return loadTraceFromReader(bytes.NewReader(traceBytes))
}

func loadTraceFromReader(r *bytes.Reader) (*ebpf.CollectionSpec, error) {
	return ebpf.LoadCollectionSpecFromReader(r)
}

var traceBytes []byte
