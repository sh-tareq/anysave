//go:build !windows

package main

import (
	"fmt"
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

// copyFileToClipboard puts the file on the clipboard the way Finder's Copy does, so it
// pastes as a file into Finder, Mail or a chat. Text files also carry their text and
// pictures their image, so pasting into a document or spreadsheet gives the contents.
// ANYSAVE_PASTEBOARD names a private pasteboard (used by tests, so the real clipboard
// is left alone).
func copyFileToClipboard(path, text, imageType string) error {
	script := `ObjC.import('AppKit');
function run(argv) {
  var path = argv[0], text = argv[1], imgType = argv[2], name = argv[3];
  var pb = name ? $.NSPasteboard.pasteboardWithName(name) : $.NSPasteboard.generalPasteboard;
  pb.clearContents;
  var item = $.NSPasteboardItem.alloc.init;
  item.setStringForType($.NSURL.fileURLWithPath(path).absoluteString, 'public.file-url');
  if (text) item.setStringForType(text, 'public.utf8-plain-text');
  if (imgType) {
    var data = $.NSData.dataWithContentsOfFile(path);
    if (data) item.setDataForType(data, imgType);
  }
  return pb.writeObjects($([item])) ? 'ok' : 'failed';
}`
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", script, "--", path, text, imageType, os.Getenv("ANYSAVE_PASTEBOARD")).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	if strings.TrimSpace(string(out)) != "ok" {
		return fmt.Errorf("the clipboard refused the file")
	}
	return nil
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

// pickFiles shows the macOS "choose files" dialog for any kind of file. Returns nil if the user cancels.
func pickFiles(multi bool) ([]string, error) {
	many := ""
	if multi {
		many = " with multiple selections allowed"
	}
	script := `try
	tell current application
		activate
		set picked to (choose file with prompt "Choose files to convert"` + many + `)
		if class of picked is not list then set picked to {picked}
		set out to ""
		repeat with f in picked
			set out to out & POSIX path of f & linefeed
		end repeat
		return out
	end tell
on error number -128
	return ""
end try`
	out, err := exec.Command("osascript", "-e", script).Output()
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, l := range strings.Split(string(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			paths = append(paths, l)
		}
	}
	return paths, nil
}

func hideWindow(cmd *exec.Cmd) {}
