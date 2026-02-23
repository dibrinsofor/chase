//go:build windows

package tracer

/*
#cgo CFLAGS: -I${SRCDIR}/e4w/include
#cgo LDFLAGS: -L${SRCDIR}/e4w/lib -lebpfapi

#include <stdlib.h>
#include <stdint.h>

// e4w API definitions
typedef int ebpf_result_t;
typedef void* ebpf_handle_t;
typedef void* fd_t;

#define EBPF_SUCCESS 0

// Simplified e4w API bindings
extern ebpf_result_t ebpf_api_initiate();
extern void ebpf_api_terminate();
extern ebpf_result_t ebpf_object_open(const char* path, ebpf_handle_t* handle);
extern ebpf_result_t ebpf_object_load(ebpf_handle_t handle);
extern ebpf_result_t ebpf_program_attach(ebpf_handle_t program, void* attach_params, ebpf_handle_t* link);
extern ebpf_result_t ebpf_link_detach(ebpf_handle_t link);
extern ebpf_result_t ebpf_object_close(ebpf_handle_t handle);
extern ebpf_result_t ebpf_map_lookup_elem(fd_t map_fd, const void* key, void* value);
extern ebpf_result_t ebpf_ring_buffer_create(fd_t map_fd, void** ring);
extern ebpf_result_t ebpf_ring_buffer_poll(void* ring, int timeout_ms);

// Event structure matching the BPF program
struct trace_event {
    uint32_t pid;
    uint32_t tid;
    uint64_t timestamp;
    uint32_t flags;
    uint8_t  op;
    uint8_t  padding[3];
    char     path[256];
};
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"unsafe"
)

type ebpfTracerWindows struct {
	cfg       Config
	handle    C.ebpf_handle_t
	link      C.ebpf_handle_t
	ring      unsafe.Pointer
	events    chan FileAccess
	collected []FileAccess
	mu        sync.Mutex
	running   bool
	done      chan struct{}
}

func newPlatformTracer(cfg Config) (Tracer, error) {
	result := C.ebpf_api_initiate()
	if result != C.EBPF_SUCCESS {
		return nil, fmt.Errorf("failed to initialize e4w API: %d", result)
	}

	return &ebpfTracerWindows{
		cfg:    cfg,
		events: make(chan FileAccess, 1024),
		done:   make(chan struct{}),
	}, nil
}

func (t *ebpfTracerWindows) Start(ctx context.Context, pid int) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.running {
		return errors.New("tracer already running")
	}

	bpfPath := C.CString("trace.o")
	defer C.free(unsafe.Pointer(bpfPath))

	result := C.ebpf_object_open(bpfPath, &t.handle)
	if result != C.EBPF_SUCCESS {
		return fmt.Errorf("failed to open eBPF object: %d", result)
	}

	result = C.ebpf_object_load(t.handle)
	if result != C.EBPF_SUCCESS {
		C.ebpf_object_close(t.handle)
		return fmt.Errorf("failed to load eBPF object: %d", result)
	}

	result = C.ebpf_program_attach(t.handle, nil, &t.link)
	if result != C.EBPF_SUCCESS {
		C.ebpf_object_close(t.handle)
		return fmt.Errorf("failed to attach eBPF program: %d", result)
	}

	t.running = true
	t.done = make(chan struct{})

	go t.readEvents(ctx)

	return nil
}

func (t *ebpfTracerWindows) readEvents(ctx context.Context) {
	defer close(t.done)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		result := C.ebpf_ring_buffer_poll(t.ring, 100)
		if result != C.EBPF_SUCCESS {
			continue
		}
	}
}

func (t *ebpfTracerWindows) processEvent(event *C.struct_trace_event) {
	path := C.GoString(&event.path[0])
	if path == "" {
		return
	}

	fa := FileAccess{
		Path:      path,
		Operation: windowsFlagsToOp(uint32(event.flags)),
		PID:       int(event.pid),
		TID:       int(event.tid),
		Timestamp: uint64(event.timestamp),
		Flags:     uint32(event.flags),
	}

	t.mu.Lock()
	t.collected = append(t.collected, fa)
	t.mu.Unlock()

	select {
	case t.events <- fa:
	default:
	}
}

func windowsFlagsToOp(flags uint32) Operation {
	const (
		GENERIC_READ  = 0x80000000
		GENERIC_WRITE = 0x40000000
	)

	if flags&GENERIC_WRITE != 0 {
		return OpWrite
	}
	return OpRead
}

func (t *ebpfTracerWindows) Stop() ([]FileAccess, []ProcessInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if !t.running {
		return t.collected, nil, nil
	}

	<-t.done

	if t.link != nil {
		C.ebpf_link_detach(t.link)
	}
	if t.handle != nil {
		C.ebpf_object_close(t.handle)
	}

	t.running = false

	// Windows tracer doesn't yet support process capture
	return t.collected, nil, nil
}

func (t *ebpfTracerWindows) Events() <-chan FileAccess {
	return t.events
}

func (t *ebpfTracerWindows) Close() error {
	C.ebpf_api_terminate()
	return nil
}
