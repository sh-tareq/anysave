//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// Run yt-dlp in its own process group so cancel can stop ffmpeg too.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func revealInFileManager(path string) {
	exec.Command("open", "-R", path).Run()
}
