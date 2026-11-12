package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestDaemonAuthStateAndPersistence(t *testing.T) {
	tmp := t.TempDir()
	oldApp := appDir
	oldDl := downloadDir
	appDir = tmp
	downloadDir = filepath.Join(tmp, "Downloads", "AnySave")
	defer func() {
		appDir = oldApp
		downloadDir = oldDl
	}()

	d := &daemonServer{
		token:       "test-secret-token-123",
		extDir:      filepath.Join("..", "extension"),
		jobs:        make(map[string]map[string]any),
		settings:    defaultDaemonSettings(),
		activeProcs: make(map[string]io.Writer),
		subscribers: make(map[chan string]struct{}),
		quitCh:      make(chan struct{}),
	}

	mux := http.NewServeMux()
	d.registerRoutes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Unauthorized ping should return 401
	resp1, err := http.Get(srv.URL + "/api/ping")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp1.StatusCode)
	}

	// 2. Authorized ping should return 200
	req2, _ := http.NewRequest("GET", srv.URL+"/api/ping", nil)
	req2.Header.Set("X-AnySave-Token", d.token)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 with token, got %d", resp2.StatusCode)
	}

	// 3. Update settings and verify settings.json persistence
	body, _ := json.Marshal(map[string]any{
		"type":  "setSettings",
		"patch": map[string]any{"clipboardWatch": true, "minSizeMb": 10},
	})
	req3, _ := http.NewRequest("POST", srv.URL+"/api/action", bytes.NewReader(body))
	req3.Header.Set("X-AnySave-Token", d.token)
	resp3, err := http.DefaultClient.Do(req3)
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()

	if _, err := os.Stat(d.settingsFilePath()); err != nil {
		t.Fatalf("expected settings.json to be persisted: %v", err)
	}

	// 4. Sync a job and verify jobs.json persistence + reload recovery
	d.updateJob("job-1", map[string]any{
		"kind":    "file",
		"title":   "ubuntu.iso",
		"status":  "downloading",
		"percent": 42.5,
	})
	if _, err := os.Stat(d.jobsFilePath()); err != nil {
		t.Fatalf("expected jobs.json to be persisted: %v", err)
	}

	// Reloading persisted state should mark in-flight downloading jobs as paused so they can be resumed.
	d2 := &daemonServer{
		jobs:     make(map[string]map[string]any),
		settings: defaultDaemonSettings(),
	}
	d2.loadPersistedState()
	if got := str(d2.jobs["job-1"], "status"); got != "paused" {
		t.Fatalf("expected reloaded in-flight job status to be paused, got %q", got)
	}
	if cw, _ := d2.settings["clipboardWatch"].(bool); !cw {
		t.Fatalf("expected reloaded clipboardWatch=true")
	}

	// 5. Verify manager.html injection of desktop-bridge.js
	respUI, err := http.Get(srv.URL + "/?token=" + d.token)
	if err != nil {
		t.Fatal(err)
	}
	uiHTML, _ := io.ReadAll(respUI.Body)
	respUI.Body.Close()
	if !strings.Contains(string(uiHTML), "/desktop-bridge.js") {
		t.Fatalf("expected manager.html served by daemon to include /desktop-bridge.js")
	}
}

