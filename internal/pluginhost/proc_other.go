//go:build !windows

package pluginhost

import "os/exec"

func hideConsoleWindow(_ *exec.Cmd) {}
