//go:build !windows

package manager

import (
	"os/exec"
	"syscall"

	"servergo/internal/apps/sandbox"
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

// terminateNS — sandbox ichidagi jarayonlarga SIGTERM. Ichkaridagi process
// group raqamlari host uchun ma'nosiz, shuning uchun PID namespace bo'yicha
// topib, har biriga alohida yuboramiz. bwrap'ning o'z "init" jarayoniga
// tegmaymiz: u ichidagi hamma chiqqanda o'zi tugaydi.
func terminateNS(inode uint64, initPID int) error {
	var firstErr error
	for _, pid := range sandbox.ProcsInNS(inode) {
		if pid == initPID {
			continue
		}
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
