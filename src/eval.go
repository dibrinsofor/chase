package src

import (
	"fmt"
	"os"
	"os/exec"
)

type ChaseEnv struct {
	shell  []string
	vars   map[string]string
	dashes []Dash
}

func (e *ChaseEnv) Shell() []string         { return e.shell }
func (e *ChaseEnv) Vars() map[string]string { return e.vars }
func (e *ChaseEnv) Dashes() []Dash          { return e.dashes }

type Dash struct {
	name         string
	declaredDeps []string
	summary      string
	cmds         []string
}

func (d Dash) Name() string            { return d.name }
func (d Dash) DeclaredDeps() []string  { return d.declaredDeps }
func (d Dash) Summary() string         { return d.summary }
func (d Dash) Cmds() []string          { return d.cmds }

func Eval(ast *Chasefile) *ChaseEnv {
	e := &ChaseEnv{
		shell:  make([]string, 0),
		vars:   make(map[string]string),
		dashes: make([]Dash, 0),
	}

	for _, s := range ast.Entries {
		switch {
		case s.Assign != nil:
			var sh []string
			if s.Assign.Key == "shell" { // if shell not provided, use sh
				for _, v := range s.Assign.Value.List {
					if v.String != nil {
						sh = append(sh, *v.String)
					}
				}
				e.shell = sh
			} else {
				if s.Assign.Value.String != nil {
					e.vars[s.Assign.Key] = *s.Assign.Value.String // trigger err if vars = [jsj, jsjs]
				}
			}
		case s.Section != nil:
			name := s.Section.Name
			dash := Dash{}
			dash.name = name

			for _, v := range s.Section.Steps {
				if v.Key == "cmds" {
					if v.Value.String != nil {
						dash.cmds = append(dash.cmds, *v.Value.String)
					}

					if len(v.Value.List) > 0 {
						cmds := []string{}

						for _, v_f := range v.Value.List {
							if v_f.String != nil {
								cmds = append(cmds, *v_f.String)
							}
						}
						dash.cmds = append(dash.cmds, cmds...)
					}
				} else if v.Key == "summary" {
					if v.Value.String != nil {
						dash.summary = *v.Value.String
					}
				} else if v.Key == "uses" {
					if v.Value.String != nil {
						dash.declaredDeps = append(dash.declaredDeps, *v.Value.String)
					} else if v.Value.Var != nil {
						dash.declaredDeps = append(dash.declaredDeps, *v.Value.Var)
					} else if len(v.Value.List) > 0 {
						for _, dep := range v.Value.List {
							if dep.String != nil {
								dash.declaredDeps = append(dash.declaredDeps, *dep.String)
							} else if dep.Var != nil {
								dash.declaredDeps = append(dash.declaredDeps, *dep.Var)
							}
						}
					}
				}
			}
			e.dashes = append(e.dashes, dash)
		default:
			panic(fmt.Errorf("chase: invalid input: %v \n", s))
		}

	}

	// use default shell
	if !shellExists(ast.Entries) || len(e.shell) == 0 {
		var s []string
		e.shell = append(s, "sh", "-c")
	}

	return e
}

func shellExists(entry []*Entry) bool {
	for _, s := range entry {
		if s.Assign != nil {
			if s.Assign.Key == "shell" {
				return true
			}
		}
	}
	return false
}

func setEnvVars(cmd *exec.Cmd, chase *ChaseEnv) {
	switch chase.shell[0] {
	case "sh", "/bin/sh":
		for key, value := range chase.vars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	case "powershell", "powershell.exe":
		for key, value := range chase.vars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("$env:%s=\"%s\"", key, value))
		}
	case "cmd", "cmd.exe":
		for key, value := range chase.vars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("set %s=%s", key, value))
		}
	default:
		// todo: should we throw error here? since we already default shell to sh?
		// if it works on their system why would we want to panic
		// panic(fmt.Errorf("chase: uncrecognized shell: %v", chase.shell[0]))
		for key, value := range chase.vars {
			cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", key, value))
		}
	}
}

func SetupEnv(chase *ChaseEnv) (*exec.Cmd, error) {
	cmd := exec.Command(chase.shell[0], chase.shell[1:]...)

	cmd.Env = os.Environ() // why are we doing this?

	setEnvVars(cmd, chase)

	switch chase.shell[0] {
	case "powershell", "powershell.exe", "cmd", "cmd.exe":
		cmd.Args = append(cmd.Args, "dir")
	default:
		cmd.Args = append(cmd.Args, "ls")
	}

	// dummy test
	err := cmd.Run()
	if err != nil {
		return cmd, err
	}

	return cmd, nil
}

func ExecDash(chase *ChaseEnv, r *string) error {
	d, exists := nameExists(chase.dashes, *r)
	if !exists {
		return fmt.Errorf("'%s' not found", *r)
	}

	deps, err := hasDeps(d, chase.dashes)
	if err != nil {
		return err
	}

	for _, dep := range deps {
		for _, dashCmds := range dep.cmds {
			cmd := exec.Command(chase.shell[0], chase.shell[1:]...)
			setEnvVars(cmd, chase)
			cmd.Args = append(cmd.Args, dashCmds)

			out, err := cmd.Output()
			if exitErr, ok := err.(*exec.ExitError); ok {
				return fmt.Errorf(string(exitErr.Stderr))
			} else if execErr, ok := err.(*exec.Error); ok {
				return fmt.Errorf(execErr.Error())
			}

			if string(out) != "" {
				fmt.Print(string(out))
			}
		}
	}

	for _, dashCmds := range d.cmds {
		cmd := exec.Command(chase.shell[0], chase.shell[1:]...)
		setEnvVars(cmd, chase)
		cmd.Args = append(cmd.Args, dashCmds)

		out, err := cmd.Output()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf(string(exitErr.Stderr))
		} else if execErr, ok := err.(*exec.Error); ok {
			return fmt.Errorf(execErr.Error())
		}

		if string(out) != "" {
			fmt.Print(string(out))
		}
	}

	return nil
}

func ExecAllDashes(chase *ChaseEnv) error {
	for _, d := range chase.dashes {
		err := ExecDash(chase, &d.name)
		if err != nil {
			return err
		}
	}
	return nil
}

func hasDeps(d Dash, ds []Dash) ([]Dash, error) {
	if len(d.declaredDeps) == 0 {
		return nil, nil
	}

	var deps []Dash
	for _, depName := range d.declaredDeps {
		dep, exists := nameExists(ds, depName)
		if !exists {
			return nil, fmt.Errorf("dependency '%s' not found", depName)
		}
		deps = append(deps, dep)
	}

	return deps, nil
}

func ListDashes(chase *ChaseEnv) {
	fmt.Println("All dashes:")
	for _, v := range chase.dashes {
		if v.summary != "" {
			fmt.Printf("  %s \t summary: '%s'\n", v.name, v.summary)
		} else {
			fmt.Printf("  %s \t --\n", v.name)
		}
	}
}

func nameExists(ds []Dash, name string) (Dash, bool) {
	for _, d := range ds {
		if d.name == name {
			return d, true
		}
	}
	return Dash{}, false
}
