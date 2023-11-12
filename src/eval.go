package src

import (
	"fmt"
	"os"
	"os/exec"
)

type ChaseEnv struct {
	shell  []string
	vars   map[string]string
	dashes []Dash // list of name -> cmds
}

// todo: change dash to sprint?
type Dash struct {
	name     string
	inherits string
	summary  string
	cmds     []string
}

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
						dash.inherits = *v.Value.String
					} else if v.Value.Var != nil {
						dash.inherits = *v.Value.Var
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

// todo: we do not want to have to set env vars twice
func ExecDash(chase *ChaseEnv, r *string) error {
	d, exists := nameExists(chase.dashes, *r)
	if !exists {
		return fmt.Errorf("'%s' not found", *r)
	}

	dep, err := hasDeps(d, chase.dashes)
	if err != nil {
		return err
	}

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
	// todo: can we run any of these in parallel?
	// todo: do not repeat commands
	// trace first full build into some IR. we can make decisions off that

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

// todo: handle multiple deps
func hasDeps(d Dash, ds []Dash) (Dash, error) {
	if d.inherits == "" {
		return Dash{}, nil
	}

	deps, exists := nameExists(ds, d.inherits)
	if !exists {
		return Dash{}, fmt.Errorf("'%s' not found", d.inherits)
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
