//go:build windows

package pluginhost

import (
	"os/exec"
	"syscall"
)

const createNoWindowFlag = 0x08000000

func hideConsoleWindow(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= createNoWindowFlag
}
