package ir

import (
	"testing"

	"github.com/dibrinsofor/chase/src/tracer"
)

func TestNewCommandID(t *testing.T) {
	id1 := NewCommandID("/usr/bin/cc1", []string{"foo.c", "-o", "foo.s"}, []string{"foo.s"})
	id2 := NewCommandID("/usr/bin/cc1", []string{"foo.c", "-o", "foo.s"}, []string{"foo.s"})
	id3 := NewCommandID("/usr/bin/cc1", []string{"bar.c", "-o", "bar.s"}, []string{"bar.s"})

	if id1 != id2 {
		t.Errorf("same inputs should produce same ID: %s != %s", id1, id2)
	}
	if id1 == id3 {
		t.Error("different inputs should produce different IDs")
	}
	if len(id1) != 16 {
		t.Errorf("CommandID length = %d, want 16", len(id1))
	}
}

func TestCommandString(t *testing.T) {
	cmd := &Command{
		Executable: "/usr/lib/gcc/x86_64-linux-gnu/11/cc1",
		Args:       []string{"foo.c", "-o", "foo.s"},
	}

	got := cmd.String()
	if got != "cc1 foo.c -o foo.s" {
		t.Errorf("Command.String() = %q, want %q", got, "cc1 foo.c -o foo.s")
	}
}

func TestBuildFromTrace(t *testing.T) {
	procs := []tracer.ProcessInfo{
		{PID: 100, PPID: 1, Comm: "cc1", Filename: "/usr/bin/cc1", Argv: []string{"cc1"}, Timestamp: 1},
		{PID: 101, PPID: 1, Comm: "as", Filename: "/usr/bin/as", Argv: []string{"as"}, Timestamp: 2},
		{PID: 102, PPID: 1, Comm: "ld", Filename: "/usr/bin/ld", Argv: []string{"ld"}, Timestamp: 3},
	}

	accesses := []tracer.FileAccess{
		// cc1 reads foo.c, writes foo.s
		{Path: "/src/foo.c", Operation: tracer.OpOpen, PID: 100, Flags: 0},
		{Path: "/tmp/foo.s", Operation: tracer.OpWrite, PID: 100, Flags: 1},
		// as reads foo.s, writes foo.o
		{Path: "/tmp/foo.s", Operation: tracer.OpOpen, PID: 101, Flags: 0},
		{Path: "/src/foo.o", Operation: tracer.OpWrite, PID: 101, Flags: 1},
		// ld reads foo.o, writes main
		{Path: "/src/foo.o", Operation: tracer.OpOpen, PID: 102, Flags: 0},
		{Path: "/src/main", Operation: tracer.OpWrite, PID: 102, Flags: 1},
	}

	g := BuildFromTrace(procs, accesses)

	if g.Len() != 3 {
		t.Errorf("graph has %d commands, want 3", g.Len())
	}

	cmds := g.Commands()
	// cc1 should have foo.c as input, foo.s as output
	cc1 := cmds[0]
	if len(cc1.Inputs) != 1 || cc1.Inputs[0] != "/src/foo.c" {
		t.Errorf("cc1 inputs = %v, want [/src/foo.c]", cc1.Inputs)
	}
	if len(cc1.Outputs) != 1 || cc1.Outputs[0] != "/tmp/foo.s" {
		t.Errorf("cc1 outputs = %v, want [/tmp/foo.s]", cc1.Outputs)
	}

	// as should read foo.s (produced by cc1)
	as := cmds[1]
	if len(as.Inputs) != 1 || as.Inputs[0] != "/tmp/foo.s" {
		t.Errorf("as inputs = %v, want [/tmp/foo.s]", as.Inputs)
	}
}

func TestCommandGraphEdges(t *testing.T) {
	procs := []tracer.ProcessInfo{
		{PID: 100, Comm: "cc1", Filename: "/usr/bin/cc1", Argv: []string{"cc1"}, Timestamp: 1},
		{PID: 101, Comm: "as", Filename: "/usr/bin/as", Argv: []string{"as"}, Timestamp: 2},
		{PID: 102, Comm: "ld", Filename: "/usr/bin/ld", Argv: []string{"ld"}, Timestamp: 3},
	}

	accesses := []tracer.FileAccess{
		{Path: "/src/foo.c", Operation: tracer.OpOpen, PID: 100, Flags: 0},
		{Path: "/tmp/foo.s", Operation: tracer.OpWrite, PID: 100, Flags: 1},
		{Path: "/tmp/foo.s", Operation: tracer.OpOpen, PID: 101, Flags: 0},
		{Path: "/src/foo.o", Operation: tracer.OpWrite, PID: 101, Flags: 1},
		{Path: "/src/foo.o", Operation: tracer.OpOpen, PID: 102, Flags: 0},
		{Path: "/src/main", Operation: tracer.OpWrite, PID: 102, Flags: 1},
	}

	g := BuildFromTrace(procs, accesses)

	// cc1 -> as (via foo.s), as -> ld (via foo.o) = 2 edges
	if g.EdgeCount() != 2 {
		t.Errorf("edge count = %d, want 2", g.EdgeCount())
	}

	// cc1's dependents should be [as]
	cc1 := g.Commands()[0]
	deps := g.Dependents(cc1.ID)
	if len(deps) != 1 {
		t.Fatalf("cc1 dependents = %d, want 1", len(deps))
	}
	if deps[0].Executable != "/usr/bin/as" {
		t.Errorf("cc1 dependent = %s, want /usr/bin/as", deps[0].Executable)
	}
}

func TestGetAffected(t *testing.T) {
	procs := []tracer.ProcessInfo{
		{PID: 100, Comm: "cc1", Filename: "/usr/bin/cc1", Argv: []string{"cc1", "foo.c"}, Timestamp: 1},
		{PID: 101, Comm: "as", Filename: "/usr/bin/as", Argv: []string{"as", "foo.s"}, Timestamp: 2},
		{PID: 102, Comm: "cc1", Filename: "/usr/bin/cc1", Argv: []string{"cc1", "bar.c"}, Timestamp: 3},
		{PID: 103, Comm: "as", Filename: "/usr/bin/as", Argv: []string{"as", "bar.s"}, Timestamp: 4},
		{PID: 104, Comm: "ld", Filename: "/usr/bin/ld", Argv: []string{"ld"}, Timestamp: 5},
	}

	accesses := []tracer.FileAccess{
		// cc1 foo.c
		{Path: "/src/foo.c", Operation: tracer.OpOpen, PID: 100, Flags: 0},
		{Path: "/src/common.h", Operation: tracer.OpOpen, PID: 100, Flags: 0},
		{Path: "/tmp/foo.s", Operation: tracer.OpWrite, PID: 100, Flags: 1},
		// as foo.s
		{Path: "/tmp/foo.s", Operation: tracer.OpOpen, PID: 101, Flags: 0},
		{Path: "/src/foo.o", Operation: tracer.OpWrite, PID: 101, Flags: 1},
		// cc1 bar.c
		{Path: "/src/bar.c", Operation: tracer.OpOpen, PID: 102, Flags: 0},
		{Path: "/src/common.h", Operation: tracer.OpOpen, PID: 102, Flags: 0},
		{Path: "/tmp/bar.s", Operation: tracer.OpWrite, PID: 102, Flags: 1},
		// as bar.s
		{Path: "/tmp/bar.s", Operation: tracer.OpOpen, PID: 103, Flags: 0},
		{Path: "/src/bar.o", Operation: tracer.OpWrite, PID: 103, Flags: 1},
		// ld
		{Path: "/src/foo.o", Operation: tracer.OpOpen, PID: 104, Flags: 0},
		{Path: "/src/bar.o", Operation: tracer.OpOpen, PID: 104, Flags: 0},
		{Path: "/src/main", Operation: tracer.OpWrite, PID: 104, Flags: 1},
	}

	g := BuildFromTrace(procs, accesses)

	// Changing foo.c should affect: cc1 foo.c, as foo.s, ld
	affected := g.GetAffected("/src/foo.c")
	if len(affected) != 3 {
		t.Errorf("affected by foo.c = %d, want 3", len(affected))
	}

	// Changing common.h should affect all 5 commands (both cc1s, both as, ld)
	affected = g.GetAffected("/src/common.h")
	if len(affected) != 5 {
		t.Errorf("affected by common.h = %d, want 5", len(affected))
	}

	// Changing bar.c should affect: cc1 bar.c, as bar.s, ld
	affected = g.GetAffected("/src/bar.c")
	if len(affected) != 3 {
		t.Errorf("affected by bar.c = %d, want 3", len(affected))
	}
}

func TestAllFiles(t *testing.T) {
	procs := []tracer.ProcessInfo{
		{PID: 100, Comm: "cc1", Filename: "/usr/bin/cc1", Argv: []string{"cc1"}, Timestamp: 1},
		{PID: 101, Comm: "as", Filename: "/usr/bin/as", Argv: []string{"as"}, Timestamp: 2},
	}

	accesses := []tracer.FileAccess{
		{Path: "/src/foo.c", Operation: tracer.OpOpen, PID: 100, Flags: 0},
		{Path: "/tmp/foo.s", Operation: tracer.OpWrite, PID: 100, Flags: 1},
		{Path: "/tmp/foo.s", Operation: tracer.OpOpen, PID: 101, Flags: 0},
		{Path: "/src/foo.o", Operation: tracer.OpWrite, PID: 101, Flags: 1},
	}

	g := BuildFromTrace(procs, accesses)
	inputs, outputs := g.AllFiles()

	if len(inputs) != 2 {
		t.Errorf("inputs count = %d, want 2 (foo.c, foo.s)", len(inputs))
	}
	if len(outputs) != 2 {
		t.Errorf("outputs count = %d, want 2 (foo.s, foo.o)", len(outputs))
	}
}

func TestShouldFilterPath(t *testing.T) {
	tests := []struct {
		path   string
		filter bool
	}{
		{"/dev/null", true},
		{"/proc/self/maps", true},
		{"/sys/devices/foo", true},
		{"/etc/ld.so.cache", true},
		{"/usr/lib/locale/C.UTF-8", true},
		{"/usr/lib/libc.so", true},
		{"/usr/lib/libc.so.6", true},
		{"/src/foo.c", false},
		{"/tmp/foo.s", false},
		{"/src/foo.o", false},
	}

	for _, tt := range tests {
		if got := shouldFilterPath(tt.path); got != tt.filter {
			t.Errorf("shouldFilterPath(%q) = %v, want %v", tt.path, got, tt.filter)
		}
	}
}

func TestEmptyBuildFromTrace(t *testing.T) {
	g := BuildFromTrace(nil, nil)
	if g.Len() != 0 {
		t.Errorf("empty graph has %d commands, want 0", g.Len())
	}
	if g.EdgeCount() != 0 {
		t.Errorf("empty graph has %d edges, want 0", g.EdgeCount())
	}
}
