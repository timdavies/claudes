package hooks

import (
	"fmt"
	"os"
	"os/exec"
)

// Run executes a hook command via `sh -c`. cmd may be empty (no-op).
// env keys are merged into the parent environment as KEY=VALUE.
func Run(name, cmd string, env map[string]string) error {
	if cmd == "" {
		return nil
	}
	c := exec.Command("sh", "-c", cmd)
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, fmt.Sprintf("%s=%s", k, v))
	}
	return c.Run()
}
