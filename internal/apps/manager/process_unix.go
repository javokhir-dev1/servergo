//go:build !windows

package manager

import (
	"os/exec"
	"syscall"
)

// setupProcAttr — jarayonni alohida process group'ga joylaydi, shunda uning
// bola jarayonlari (masalan `sh -c` ostidagi haqiqiy dastur) birga to'xtaydi.
func setupProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// terminate — butun process group'ga SIGTERM (yumshoq to'xtatish).
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

// forceKill — SIGKILL (majburiy to'xtatish).
func forceKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
