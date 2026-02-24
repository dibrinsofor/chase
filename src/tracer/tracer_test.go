package tracer

import (
	"testing"
)

func TestOperationString(t *testing.T) {
	tests := []struct {
		op   Operation
		want string
	}{
		{OpRead, "read"},
		{OpWrite, "write"},
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

	// fsatrace may not be built yet, so we allow error
	if err != nil {
		t.Skipf("fsatrace binary not found: %v", err)
	}
	if tracer == nil {
		t.Error("expected tracer instance")
	}
}

func TestFileAccess(t *testing.T) {
	fa := FileAccess{
		Path:      "/tmp/test.txt",
		Operation: OpRead,
	}

	if fa.Path != "/tmp/test.txt" {
		t.Errorf("Path = %s, want /tmp/test.txt", fa.Path)
	}
	if fa.Operation != OpRead {
		t.Errorf("Operation = %v, want OpRead", fa.Operation)
	}
}

func TestParseOutput(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []FileAccess
	}{
		{
			name:  "read operation",
			input: "r|/path/to/file\n",
			expected: []FileAccess{
				{Path: "/path/to/file", Operation: OpRead},
			},
		},
		{
			name:  "write operation",
			input: "w|/path/to/output\n",
			expected: []FileAccess{
				{Path: "/path/to/output", Operation: OpWrite},
			},
		},
		{
			name:  "move operation (treated as write)",
			input: "m|/new/path|/old/path\n",
			expected: []FileAccess{
				{Path: "/new/path", Operation: OpWrite},
			},
		},
		{
			name:  "stat operation (treated as read)",
			input: "q|/stated/file\n",
			expected: []FileAccess{
				{Path: "/stated/file", Operation: OpRead},
			},
		},
		{
			name:     "delete operation (ignored)",
			input:    "d|/deleted/file\n",
			expected: nil,
		},
		{
			name:  "multiple operations",
			input: "r|/input.c\nw|/output.o\nq|/headers/h.h\n",
			expected: []FileAccess{
				{Path: "/input.c", Operation: OpRead},
				{Path: "/output.o", Operation: OpWrite},
				{Path: "/headers/h.h", Operation: OpRead},
			},
		},
		{
			name:  "deduplication",
			input: "r|/file.c\nr|/file.c\nw|/file.o\nw|/file.o\n",
			expected: []FileAccess{
				{Path: "/file.c", Operation: OpRead},
				{Path: "/file.o", Operation: OpWrite},
			},
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:     "malformed line",
			input:    "invalid\n",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseOutput([]byte(tt.input))

			if len(result) != len(tt.expected) {
				t.Fatalf("got %d accesses, want %d", len(result), len(tt.expected))
			}

			for i, want := range tt.expected {
				got := result[i]
				if got.Path != want.Path {
					t.Errorf("access[%d].Path = %s, want %s", i, got.Path, want.Path)
				}
				if got.Operation != want.Operation {
					t.Errorf("access[%d].Operation = %v, want %v", i, got.Operation, want.Operation)
				}
			}
		})
	}
}
