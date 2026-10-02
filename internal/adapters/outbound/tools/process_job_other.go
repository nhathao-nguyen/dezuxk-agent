//go:build !windows

package tools

import (
	"os/exec"
	"syscall"
)

type processJobGroup struct {
	pgid int
}

func setupProcessGroup(cmd *exec.Cmd) (*processJobGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return &processJobGroup{}, nil
}

func (g *processJobGroup) attachProcess(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		g.pgid = cmd.Process.Pid
	}
	return nil
}

func (g *processJobGroup) dispose(forceKill bool) {
	if g != nil && g.pgid > 0 && forceKill {
		_ = syscall.Kill(-g.pgid, syscall.SIGKILL)
	}
}
