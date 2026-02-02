//go:build linux

package tracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
	"golang.org/x/sys/unix"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -cc clang -cflags "-O2 -g -Wall" -target amd64,arm64 trace ./bpf/trace.c

const maxPathLen = 256

type bpfEvent struct {
	Pid       uint32
	Tid       uint32
	Timestamp uint64
	Flags     uint32
	Op        uint8
	_         [3]uint8
	Path      [maxPathLen]byte
}

type ebpfTracerLinux struct {
	cfg       Config
	objs      *traceObjects
	links     []link.Link
	reader    *ringbuf.Reader
	events    chan FileAccess
	collected []FileAccess
	mu        sync.Mutex
	running   bool
	done      chan struct{}
}

func newPlatformTracer(cfg Config) (Tracer, error) {
	return &ebpfTracerLinux{
		cfg:    cfg,
		events: make(chan FileAccess, 1024),
		done:   make(chan struct{}),
	}, nil
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

	if pid > 0 {
		if err := t.objs.TargetPids.Put(uint32(pid), uint32(1)); err != nil {
			t.cleanup()
			return fmt.Errorf("add target pid: %w", err)
		}
	}

	t.running = true
	t.done = make(chan struct{})

	go t.readEvents(ctx)

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

		fa := FileAccess{
			Path:      path,
			Operation: flagsToOp(event.Flags),
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

func (t *ebpfTracerLinux) Stop() ([]FileAccess, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return t.collected, nil
	}

	if t.reader != nil {
		t.reader.Close()
	}

	<-t.done

	t.cleanup()
	t.running = false

	return t.collected, nil
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

func (t *ebpfTracerLinux) Events() <-chan FileAccess {
	return t.events
}

type traceObjects struct {
	TraceOpenatEnter *ebpf.Program `ebpf:"trace_openat_enter"`
	TraceFork        *ebpf.Program `ebpf:"trace_fork"`
	TraceExit        *ebpf.Program `ebpf:"trace_exit"`
	Events           *ebpf.Map     `ebpf:"events"`
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
	if o.Events != nil {
		o.Events.Close()
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
