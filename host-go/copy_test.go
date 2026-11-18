//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Copies onto a private pasteboard (never the real clipboard) and reads it back.
func TestCopyFile(t *testing.T) {
	tmp := t.TempDir()
	old := downloadDir
	downloadDir = filepath.Join(tmp, "Downloads", "AnySave")
	defer func() { downloadDir = old }()
	os.MkdirAll(downloadDir, 0o755)
	t.Setenv("ANYSAVE_PASTEBOARD", "com.anysave.test.copy")
	csv := filepath.Join(downloadDir, "export.csv")
	os.WriteFile(csv, append([]byte{0xEF, 0xBB, 0xBF}, "id,name\n1,Café\n"...), 0o644)

	r := actionCopyFile(map[string]any{"file": csv})
	if r["ok"] != true || r["asText"] != true {
		t.Fatalf("copy: %v", r)
	}
	read := `ObjC.import('AppKit');
function run(a){var pb=$.NSPasteboard.pasteboardWithName(a[0]);
return ObjC.unwrap(pb.stringForType('public.file-url'))+'|'+ObjC.unwrap(pb.stringForType('public.utf8-plain-text'));}`
	out, err := exec.Command("osascript", "-l", "JavaScript", "-e", read, "--", "com.anysave.test.copy").CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got := strings.TrimSpace(string(out))
	if !strings.HasPrefix(got, "file://") || !strings.Contains(got, "export.csv|id,name\n1,Café") {
		t.Fatalf("pasteboard has %q", got)
	}
	// Nothing outside Downloads.
	outside := filepath.Join(tmp, "secret.txt")
	os.WriteFile(outside, []byte("x"), 0o644)
	if r := actionCopyFile(map[string]any{"file": outside}); r["ok"] != false {
		t.Fatalf("copied a file outside Downloads: %v", r)
	}
	if r := actionCopyFile(map[string]any{"file": downloadDir + "/../../secret.txt"}); r["ok"] != false {
		t.Fatalf("copied via ..: %v", r)
	}
}
