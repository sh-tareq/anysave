//go:build !windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// userDownloadsDir returns the user's Downloads folder on Unix/macOS.
func userDownloadsDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Downloads")
}

// Run yt-dlp in its own process group so cancel can stop ffmpeg too.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachChildJob(cmd *exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
}

func revealInFileManager(path string) {
	exec.Command("open", "-R", path).Run()
}

// openWithDefaultApp opens a file in the app the user picked for its type.
func openWithDefaultApp(path string) error {
	return exec.Command("open", path).Run()
}

// pickFile shows the macOS "choose a file" dialog. Returns "" if the user cancels.
func pickFile() (string, error) {
	// The helper runs in the background (Chrome starts it), so macOS would open the picker
	// behind Chrome. Bringing the script's own process to the front first makes it appear on top.
	script := `try
	tell current application
		activate
		return POSIX path of (choose file with prompt "Choose a video or audio file to convert" of type {"public.movie", "public.audio"})
	end tell
on error number -128
	return ""
end try`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
