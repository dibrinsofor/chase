//go:build !linux && !windows

package tracer

import (
	"fmt"
	"runtime"
)

func newPlatformTracer(cfg Config) (Tracer, error) {
	return nil, fmt.Errorf("eBPF tracer not supported on %s", runtime.GOOS)
}
