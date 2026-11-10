//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

const createNoWindow = 0x08000000

// Don't flash a console window for yt-dlp / ffmpeg.
func setProcAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}

// yt-dlp.exe is a launcher that starts a second process, and ffmpeg runs
// under it, so kill the whole tree.
func killTree(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	k := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	k.Run()
}

func revealInFileManager(path string) {
	exec.Command("explorer", "/select,", path).Run()
}

// openWithDefaultApp opens a file in the app the user picked for its type
// (ShellExecute, the same as double-clicking it in Explorer).
func openWithDefaultApp(path string) error {
	c := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", path)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	return c.Start()
}

// pickFile shows the Windows "Open" dialog. Returns "" if the user cancels.
func pickFile() (string, error) {
	script := `Add-Type -AssemblyName System.Windows.Forms
$d = New-Object System.Windows.Forms.OpenFileDialog
$d.Title = "Choose a video or audio file to convert"
$d.Filter = "Video and audio|*.mp4;*.mkv;*.webm;*.mov;*.avi;*.m4v;*.flv;*.wmv;*.ts;*.mp3;*.m4a;*.wav;*.flac;*.ogg;*.opus;*.aac|All files|*.*"
$f = New-Object System.Windows.Forms.Form -Property @{TopMost = $true; ShowInTaskbar = $false}
if ($d.ShowDialog($f) -eq "OK") { [Console]::OutputEncoding = [Text.Encoding]::UTF8; Write-Output $d.FileName }`
	c := exec.Command("powershell", "-NoProfile", "-STA", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
	out, err := c.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
