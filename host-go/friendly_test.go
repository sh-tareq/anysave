package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFriendlyError(t *testing.T) {
	cases := map[string]string{
		`[generic] Got HTTP Error 403 caused by Cloudflare anti-bot challenge; try again with --extractor-args "generic:impersonate"`: "blocks automated downloads",
		"[generic] Unsupported URL: https://example.com/": "No downloadable video",
		"This video is DRM protected":                     "DRM-protected",
		"HTTP Error 404: Not Found":                       "wasn't found (404)",
		"Some other error":                                "Some other error",
	}
	for in, want := range cases {
		if got := friendlyError(in); !strings.Contains(got, want) {
			t.Errorf("friendlyError(%q) = %q, want it to contain %q", in, got, want)
		}
	}
}

func TestLogRotationAndDiagnostics(t *testing.T) {
	tmp := t.TempDir()
	oldApp := appDir
	oldDl := downloadDir
	appDir = tmp
	downloadDir = filepath.Join(tmp, "Downloads", "AnySave")
	defer func() {
		appDir = oldApp
		downloadDir = oldDl
	}()

	logPath := filepath.Join(tmp, "vidgrab.log")
	if err := os.WriteFile(logPath, make([]byte, maxLogBytes+10), 0o600); err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	logf("test entry from %s/secret", home)

	if _, err := os.Stat(logPath + ".1"); err != nil {
		t.Fatalf("expected rotated log vidgrab.log.1 to exist: %v", err)
	}
	res := actionDiagnostics(map[string]any{"extensionVersion": "1.0.5"})
	if ok, _ := res["ok"].(bool); !ok {
		t.Fatalf("expected diagnostics ok=true, got %v", res)
	}
	txt, _ := res["text"].(string)
	if !strings.Contains(txt, "Extension: 1.0.5") || !strings.Contains(txt, "Helper:    "+helperVersion) {
		t.Errorf("unexpected diagnostics text:\n%s", txt)
	}
	if home != "" && strings.Contains(txt, home) {
		t.Errorf("diagnostics text leaked raw home dir %q:\n%s", home, txt)
	}
}
