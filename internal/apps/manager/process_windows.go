//go:build windows

package manager

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setupProcAttr — konsol oynasi ochilmasin va jarayon alohida guruhda bo'lsin.
func setupProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// terminate — Windows'da SIGTERM yo'q. taskkill /T bola jarayonlar bilan
// birga yumshoq yopilish so'rovini yuboradi.
func terminate(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}

// forceKill — /F bilan majburiy to'xtatish.
func forceKill(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.Command("taskkill", "/pid", strconv.Itoa(cmd.Process.Pid), "/t", "/f")
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return kill.Run()
}
