//go:build !windows

package stt

import "os/exec"

func configureSubprocess(_ *exec.Cmd) {}
