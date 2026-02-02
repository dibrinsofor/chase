package tracer

import (
	"runtime"
	"testing"
)

func TestOperationString(t *testing.T) {
	tests := []struct {
		op   Operation
		want string
	}{
		{OpRead, "read"},
		{OpWrite, "write"},
		{OpOpen, "open"},
		{OpClose, "close"},
		{Operation(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.op.String(); got != tt.want {
			t.Errorf("Operation(%d).String() = %s, want %s", tt.op, got, tt.want)
		}
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.FilterPID != 0 {
		t.Errorf("FilterPID = %d, want 0", cfg.FilterPID)
	}
	if !cfg.FollowChildren {
		t.Error("FollowChildren should be true by default")
	}
	if !cfg.CaptureReads {
		t.Error("CaptureReads should be true by default")
	}
	if !cfg.CaptureWrites {
		t.Error("CaptureWrites should be true by default")
	}
}

func TestNewTracer(t *testing.T) {
	cfg := DefaultConfig()
	tracer, err := New(cfg)

	switch runtime.GOOS {
	case "linux", "windows":
		if err != nil {
			t.Skipf("eBPF may require privileges: %v", err)
		}
		if tracer == nil {
			t.Error("expected tracer on supported platform")
		}
	default:
		if err == nil {
			t.Errorf("expected error on unsupported platform %s", runtime.GOOS)
		}
		if tracer != nil {
			t.Error("expected nil tracer on unsupported platform")
		}
	}
}

func TestFileAccess(t *testing.T) {
	fa := FileAccess{
		Path:      "/tmp/test.txt",
		Operation: OpRead,
		PID:       1234,
		TID:       1234,
		Timestamp: 1000000,
		Flags:     0,
	}

	if fa.Path != "/tmp/test.txt" {
		t.Errorf("Path = %s, want /tmp/test.txt", fa.Path)
	}
	if fa.Operation != OpRead {
		t.Errorf("Operation = %v, want OpRead", fa.Operation)
	}
	if fa.PID != 1234 {
		t.Errorf("PID = %d, want 1234", fa.PID)
	}
}
