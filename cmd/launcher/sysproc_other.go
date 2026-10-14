//go:build !windows

package main

import "os/exec"

func setSysProcAttr(_ *exec.Cmd) {}
