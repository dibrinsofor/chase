package src

import (
	"reflect"
	"testing"
)

type ExpectedEnv struct {
	Shell  []string
	Vars   map[string]string
	Dashes []ExpectedDash
}

type ExpectedDash struct {
	Name         string
	DeclaredDeps []string
	Summary      string
	Cmds         []string
}

func toExpectedEnv(e *ChaseEnv) ExpectedEnv {
	ee := ExpectedEnv{
		Shell: e.shell,
		Vars:  e.vars,
	}
	for _, d := range e.dashes {
		ee.Dashes = append(ee.Dashes, ExpectedDash{
			Name:         d.name,
			DeclaredDeps: d.declaredDeps,
			Summary:      d.summary,
			Cmds:         d.cmds,
		})
	}
	return ee
}

func strPtr(s string) *string {
	return &s
}

func buildChasefile(assigns []struct{ key, val string }, sections []ExpectedDash) *Chasefile {
	cf := &Chasefile{}

	for _, a := range assigns {
		entry := &Entry{
			Assign: &Assign{
				Key:   a.key,
				Value: &Value{String: strPtr(a.val)},
			},
		}
		cf.Entries = append(cf.Entries, entry)
	}

	for _, s := range sections {
		sec := &Section{Name: s.Name}
		if s.Summary != "" {
			sec.Steps = append(sec.Steps, &Step{Key: "summary", Value: &Value{String: strPtr(s.Summary)}})
		}
		if len(s.DeclaredDeps) > 0 {
			var list []*Value
			for _, dep := range s.DeclaredDeps {
				list = append(list, &Value{Var: strPtr(dep)})
			}
			sec.Steps = append(sec.Steps, &Step{Key: "uses", Value: &Value{List: list}})
		}
		for _, cmd := range s.Cmds {
			sec.Steps = append(sec.Steps, &Step{Key: "cmds", Value: &Value{String: strPtr(cmd)}})
		}
		cf.Entries = append(cf.Entries, &Entry{Section: sec})
	}

	return cf
}

func buildChasefileWithShell(shell []string, vars map[string]string, sections []ExpectedDash) *Chasefile {
	cf := &Chasefile{}

	if len(shell) > 0 {
		var list []*Value
		for _, s := range shell {
			list = append(list, &Value{String: strPtr(s)})
		}
		cf.Entries = append(cf.Entries, &Entry{
			Assign: &Assign{Key: "shell", Value: &Value{List: list}},
		})
	}

	for k, v := range vars {
		cf.Entries = append(cf.Entries, &Entry{
			Assign: &Assign{Key: k, Value: &Value{String: strPtr(v)}},
		})
	}

	for _, s := range sections {
		sec := &Section{Name: s.Name}
		if s.Summary != "" {
			sec.Steps = append(sec.Steps, &Step{Key: "summary", Value: &Value{String: strPtr(s.Summary)}})
		}
		if len(s.DeclaredDeps) > 0 {
			var list []*Value
			for _, dep := range s.DeclaredDeps {
				list = append(list, &Value{Var: strPtr(dep)})
			}
			sec.Steps = append(sec.Steps, &Step{Key: "uses", Value: &Value{List: list}})
		}
		if len(s.Cmds) > 0 {
			var list []*Value
			for _, cmd := range s.Cmds {
				list = append(list, &Value{String: strPtr(cmd)})
			}
			sec.Steps = append(sec.Steps, &Step{Key: "cmds", Value: &Value{List: list}})
		}
		cf.Entries = append(cf.Entries, &Entry{Section: sec})
	}

	return cf
}

func TestEval(t *testing.T) {
	tests := []struct {
		name string
		ast  *Chasefile
		want ExpectedEnv
	}{
		{
			name: "empty chasefile defaults to sh",
			ast:  &Chasefile{},
			want: ExpectedEnv{
				Shell:  []string{"sh", "-c"},
				Vars:   map[string]string{},
				Dashes: nil,
			},
		},
		{
			name: "custom shell",
			ast:  buildChasefileWithShell([]string{"bash", "-c"}, nil, nil),
			want: ExpectedEnv{
				Shell:  []string{"bash", "-c"},
				Vars:   map[string]string{},
				Dashes: nil,
			},
		},
		{
			name: "variables",
			ast:  buildChasefileWithShell(nil, map[string]string{"version": "1.0.0", "env": "prod"}, nil),
			want: ExpectedEnv{
				Shell:  []string{"sh", "-c"},
				Vars:   map[string]string{"version": "1.0.0", "env": "prod"},
				Dashes: nil,
			},
		},
		{
			name: "single section",
			ast: buildChasefileWithShell(nil, nil, []ExpectedDash{
				{Name: "build", Summary: "build it", Cmds: []string{"go build"}},
			}),
			want: ExpectedEnv{
				Shell: []string{"sh", "-c"},
				Vars:  map[string]string{},
				Dashes: []ExpectedDash{
					{Name: "build", Summary: "build it", Cmds: []string{"go build"}},
				},
			},
		},
		{
			name: "section with dependency",
			ast: buildChasefileWithShell(nil, nil, []ExpectedDash{
				{Name: "build", Cmds: []string{"go build"}},
				{Name: "test", DeclaredDeps: []string{"build"}, Cmds: []string{"go test"}},
			}),
			want: ExpectedEnv{
				Shell: []string{"sh", "-c"},
				Vars:  map[string]string{},
				Dashes: []ExpectedDash{
					{Name: "build", Cmds: []string{"go build"}},
					{Name: "test", DeclaredDeps: []string{"build"}, Cmds: []string{"go test"}},
				},
			},
		},
		{
			name: "section with multiple cmds",
			ast: buildChasefileWithShell(nil, nil, []ExpectedDash{
				{Name: "lint", Cmds: []string{"go fmt", "go vet", "golint"}},
			}),
			want: ExpectedEnv{
				Shell: []string{"sh", "-c"},
				Vars:  map[string]string{},
				Dashes: []ExpectedDash{
					{Name: "lint", Cmds: []string{"go fmt", "go vet", "golint"}},
				},
			},
		},
		{
			name: "full chasefile",
			ast: buildChasefileWithShell(
				[]string{"bash", "-c"},
				map[string]string{"version": "2.0"},
				[]ExpectedDash{
					{Name: "build", Summary: "compile", Cmds: []string{"go build -o app"}},
					{Name: "test", DeclaredDeps: []string{"build"}, Summary: "run tests", Cmds: []string{"go test ./..."}},
				},
			),
			want: ExpectedEnv{
				Shell: []string{"bash", "-c"},
				Vars:  map[string]string{"version": "2.0"},
				Dashes: []ExpectedDash{
					{Name: "build", Summary: "compile", Cmds: []string{"go build -o app"}},
					{Name: "test", DeclaredDeps: []string{"build"}, Summary: "run tests", Cmds: []string{"go test ./..."}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := toExpectedEnv(Eval(tt.ast))
			if !reflect.DeepEqual(got.Shell, tt.want.Shell) {
				t.Errorf("shell: got %v, want %v", got.Shell, tt.want.Shell)
			}
			if !reflect.DeepEqual(got.Vars, tt.want.Vars) {
				t.Errorf("vars: got %v, want %v", got.Vars, tt.want.Vars)
			}
			if !reflect.DeepEqual(got.Dashes, tt.want.Dashes) {
				t.Errorf("dashes: got %+v, want %+v", got.Dashes, tt.want.Dashes)
			}
		})
	}
}

func TestShellExists(t *testing.T) {
	tests := []struct {
		name    string
		entries []*Entry
		want    bool
	}{
		{
			name:    "empty entries",
			entries: nil,
			want:    false,
		},
		{
			name: "no shell entry",
			entries: []*Entry{
				{Assign: &Assign{Key: "version", Value: &Value{String: strPtr("1.0")}}},
			},
			want: false,
		},
		{
			name: "has shell entry",
			entries: []*Entry{
				{Assign: &Assign{Key: "shell", Value: &Value{}}},
			},
			want: true,
		},
		{
			name: "shell among other entries",
			entries: []*Entry{
				{Assign: &Assign{Key: "version", Value: &Value{String: strPtr("1.0")}}},
				{Assign: &Assign{Key: "shell", Value: &Value{}}},
				{Section: &Section{Name: "build"}},
			},
			want: true,
		},
		{
			name: "section only",
			entries: []*Entry{
				{Section: &Section{Name: "build"}},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shellExists(tt.entries)
			if got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNameExists(t *testing.T) {
	dashes := []Dash{
		{name: "build", cmds: []string{"go build"}},
		{name: "test", cmds: []string{"go test"}},
		{name: "deploy", cmds: []string{"deploy.sh"}},
	}

	tests := []struct {
		name     string
		dashes   []Dash
		lookup   string
		wantDash Dash
		wantOk   bool
	}{
		{
			name:     "find existing dash",
			dashes:   dashes,
			lookup:   "build",
			wantDash: Dash{name: "build", cmds: []string{"go build"}},
			wantOk:   true,
		},
		{
			name:     "find another existing dash",
			dashes:   dashes,
			lookup:   "test",
			wantDash: Dash{name: "test", cmds: []string{"go test"}},
			wantOk:   true,
		},
		{
			name:     "dash not found",
			dashes:   dashes,
			lookup:   "nonexistent",
			wantDash: Dash{},
			wantOk:   false,
		},
		{
			name:     "empty dashes",
			dashes:   nil,
			lookup:   "build",
			wantDash: Dash{},
			wantOk:   false,
		},
		{
			name:     "empty lookup",
			dashes:   dashes,
			lookup:   "",
			wantDash: Dash{},
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDash, gotOk := nameExists(tt.dashes, tt.lookup)
			if gotOk != tt.wantOk {
				t.Errorf("ok: got %v, want %v", gotOk, tt.wantOk)
			}
			if !reflect.DeepEqual(gotDash, tt.wantDash) {
				t.Errorf("dash: got %+v, want %+v", gotDash, tt.wantDash)
			}
		})
	}
}

func TestHasDeps(t *testing.T) {
	dashes := []Dash{
		{name: "base", cmds: []string{"echo base"}},
		{name: "build", declaredDeps: []string{"base"}, cmds: []string{"go build"}},
		{name: "test", declaredDeps: []string{"build"}, cmds: []string{"go test"}},
		{name: "standalone", cmds: []string{"echo standalone"}},
		{name: "multi", declaredDeps: []string{"base", "standalone"}, cmds: []string{"multi"}},
	}

	tests := []struct {
		name     string
		dash     Dash
		dashes   []Dash
		wantDeps []Dash
		wantErr  bool
	}{
		{
			name:     "no dependency",
			dash:     Dash{name: "standalone", cmds: []string{"echo"}},
			dashes:   dashes,
			wantDeps: nil,
			wantErr:  false,
		},
		{
			name:     "has single dependency",
			dash:     Dash{name: "build", declaredDeps: []string{"base"}},
			dashes:   dashes,
			wantDeps: []Dash{{name: "base", cmds: []string{"echo base"}}},
			wantErr:  false,
		},
		{
			name:     "has multiple dependencies",
			dash:     Dash{name: "multi", declaredDeps: []string{"base", "standalone"}},
			dashes:   dashes,
			wantDeps: []Dash{{name: "base", cmds: []string{"echo base"}}, {name: "standalone", cmds: []string{"echo standalone"}}},
			wantErr:  false,
		},
		{
			name:     "dependency not found",
			dash:     Dash{name: "broken", declaredDeps: []string{"nonexistent"}},
			dashes:   dashes,
			wantDeps: nil,
			wantErr:  true,
		},
		{
			name:     "empty deps",
			dash:     Dash{name: "test", declaredDeps: nil},
			dashes:   dashes,
			wantDeps: nil,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotDeps, err := hasDeps(tt.dash, tt.dashes)
			if (err != nil) != tt.wantErr {
				t.Errorf("error: got %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(gotDeps, tt.wantDeps) {
				t.Errorf("deps: got %+v, want %+v", gotDeps, tt.wantDeps)
			}
		})
	}
}
