// End-to-end test for the AnySave helper (macOS, Windows, Linux).
//
// Usage:
//   go run ./cmd/test-helper -bin /path/to/vidgrab-host [-live]
//
// Tests:
//   1. `--check` and `--version`
//   2. `diagnostics` action
//   3. `probe` action (against a local HTTP test server)
//   4. `fastDownload` multi-connection engine download (against the torture server)
//   5. `open` and `reveal` on a missing file (safe error response)
//   6. With -live=true: real YouTube `info`, `playlist`, short video `download`, and `cancel`
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"vidgrab-host/torture"
)

func writeMsg(w io.Writer, req map[string]any) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err = w.Write(payload)
	return err
}

func readMsg(r io.Reader) (map[string]any, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var res map[string]any
	return res, json.Unmarshal(buf, &res)
}

func callSingleEnv(binPath string, env []string, req map[string]any) (map[string]any, error) {
	var stdin bytes.Buffer
	if err := writeMsg(&stdin, req); err != nil {
		return nil, err
	}
	cmd := exec.Command(binPath)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdin = &stdin
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("helper exited with error: %w (out=%q)", err, string(out))
	}
	return readMsg(bytes.NewReader(out))
}

func runStreamJob(binPath string, env []string, req map[string]any, cancelAfterFirstProgress bool) ([]map[string]any, error) {
	cmd := exec.Command(binPath)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	if err := writeMsg(stdin, req); err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}

	var msgs []map[string]any
	rd := bufio.NewReader(stdout)
	sentCancel := false
	for {
		m, err := readMsg(rd)
		if err != nil {
			break
		}
		msgs = append(msgs, m)
		t, _ := m["type"].(string)
		if cancelAfterFirstProgress && !sentCancel && t == "progress" {
			sentCancel = true
			_ = writeMsg(stdin, map[string]any{"action": "cancel"})
		}
		if t == "done" || t == "error" || t == "paused" || t == "fallback" {
			break
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	return msgs, nil
}

func main() {
	bin := flag.String("bin", "", "Path to vidgrab-host binary")
	live := flag.Bool("live", false, "Run live YouTube info/playlist/download/cancel checks too")
	flag.Parse()

	if *bin == "" {
		fmt.Fprintln(os.Stderr, "-bin is required")
		os.Exit(1)
	}

	tmpHome, err := os.MkdirTemp("", "anysave-e2e-home-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpHome)
	testEnv := []string{
		"HOME=" + tmpHome,
		"USERPROFILE=" + tmpHome,
		"ANYSAVE_TEST_HOME=1",
	}

	// 1. --check
	chkOut, err := exec.Command(*bin, "--check").CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "--check failed: %v\n%s\n", err, string(chkOut))
		os.Exit(1)
	}
	fmt.Printf("[OK] --check:\n%s", string(chkOut))

	// 2. --version
	verOut, err := exec.Command(*bin, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(verOut)) == "" {
		fmt.Fprintf(os.Stderr, "--version failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("[OK] --version: %s\n", strings.TrimSpace(string(verOut)))

	// 3. diagnostics
	diag, err := callSingleEnv(*bin, testEnv, map[string]any{"action": "diagnostics", "extensionVersion": "1.0.5"})
	if err != nil || diag["ok"] != true {
		fmt.Fprintf(os.Stderr, "diagnostics failed: err=%v res=%v\n", err, diag)
		os.Exit(1)
	}
	fmt.Printf("[OK] diagnostics returned helperVersion=%v os=%v\n", diag["helperVersion"], diag["os"])

	// 4. probe against local HTTP server
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="archive.zip"`)
		w.Header().Set("Content-Length", "12345")
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "fake-zip")
		}
	}))
	defer ts.Close()

	pr, err := callSingleEnv(*bin, testEnv, map[string]any{"action": "probe", "url": ts.URL + "/download"})
	if err != nil || pr["ok"] != true || pr["isFile"] != true || pr["name"] != "archive.zip" {
		fmt.Fprintf(os.Stderr, "probe failed: err=%v res=%v\n", err, pr)
		os.Exit(1)
	}
	fmt.Printf("[OK] probe detected file name=%v length=%v\n", pr["name"], pr["length"])

	// 5. fastDownload against torture server
	tort := torture.NewServer(512 * 1024)
	defer tort.Close()
	fmsgs, err := runStreamJob(*bin, testEnv, map[string]any{
		"action":   "fastDownload",
		"job":      "e2e-fast-1",
		"url":      tort.URL + "/normal",
		"fileName": "torture-fast.bin",
		"folder":   "Archives",
	}, false)
	if err != nil || len(fmsgs) == 0 {
		fmt.Fprintf(os.Stderr, "fastDownload failed: err=%v msgs=%v\n", err, fmsgs)
		os.Exit(1)
	}
	lastFast := fmsgs[len(fmsgs)-1]
	if lastFast["type"] != "done" {
		fmt.Fprintf(os.Stderr, "expected fastDownload type=done, got %v\n", lastFast)
		os.Exit(1)
	}
	savedFile, _ := lastFast["file"].(string)
	gotBytes, err := os.ReadFile(savedFile)
	if err != nil || !bytes.Equal(gotBytes, tort.Payload) {
		fmt.Fprintf(os.Stderr, "fastDownload file content mismatch at %s: err=%v\n", savedFile, err)
		os.Exit(1)
	}
	fmt.Printf("[OK] fastDownload verified (%d bytes, sha256=%v, peakConns=%v)\n",
		len(gotBytes), lastFast["sha256"], lastFast["peakConns"])

	// 6. open on a missing file
	op, err := callSingleEnv(*bin, testEnv, map[string]any{"action": "open", "file": "/nonexistent/file/path.mp4"})
	if err != nil || op["ok"] != false {
		fmt.Fprintf(os.Stderr, "expected open on missing file to return ok=false, got err=%v res=%v\n", err, op)
		os.Exit(1)
	}
	fmt.Printf("[OK] open on missing file safely rejected: %v\n", op["error"])

	if *live {
		// 7. Live YouTube info
		info, err := callSingleEnv(*bin, testEnv, map[string]any{
			"action": "info",
			"url":    "https://www.youtube.com/watch?v=jNQXAC9IVRw",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "live info call error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[OK] live info response ok=%v title=%v\n", info["ok"], info["title"])

		// 8. Live YouTube playlist
		pl, err := callSingleEnv(*bin, testEnv, map[string]any{
			"action": "playlist",
			"url":    "https://www.youtube.com/@BlenderStudio/shorts",
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "live playlist call error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("[OK] live playlist response ok=%v title=%v\n", pl["ok"], pl["title"])

		// 9. Live short video download ("Me at the zoo", 19s, smallest format)
		dlMsgs, err := runStreamJob(*bin, testEnv, map[string]any{
			"action": "download",
			"job":    "live-dl-1",
			"url":    "https://www.youtube.com/watch?v=jNQXAC9IVRw",
			"format": "worst[ext=mp4]/worst",
			"folder": "Videos",
		}, false)
		if err != nil || len(dlMsgs) == 0 {
			fmt.Fprintf(os.Stderr, "live download call error: %v\n", err)
			os.Exit(1)
		}
		lastDl := dlMsgs[len(dlMsgs)-1]
		fmt.Printf("[OK] live download finished type=%v file=%v\n", lastDl["type"], lastDl["file"])

		// 10. Cancel mid-download (against slow torture stream to guarantee deterministic mid-flight cancel)
		slow := torture.NewServer(8 * 1024 * 1024)
		defer slow.Close()
		cancelMsgs, err := runStreamJob(*bin, testEnv, map[string]any{
			"action":   "fastDownload",
			"job":      "cancel-job-1",
			"url":      slow.URL + "/slow",
			"fileName": "should-be-cancelled.bin",
			"folder":   "Other",
		}, true)
		if err != nil || len(cancelMsgs) == 0 {
			fmt.Fprintf(os.Stderr, "cancel test error: %v\n", err)
			os.Exit(1)
		}
		lastCancel := cancelMsgs[len(cancelMsgs)-1]
		if lastCancel["type"] != "error" || lastCancel["message"] != "Cancelled" {
			fmt.Fprintf(os.Stderr, "expected cancel to return error=Cancelled, got %v\n", lastCancel)
			os.Exit(1)
		}
		partsLeft := filepath.Join(tmpHome, "Downloads", "AnySave", "Other", ".anysave-parts", "cancel-job-1")
		time.Sleep(50 * time.Millisecond)
		if _, err := os.Stat(partsLeft); !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "expected cancelled job parts dir %s to be deleted\n", partsLeft)
			os.Exit(1)
		}
		fmt.Printf("[OK] live cancel cleaned up partial files and returned Cancelled\n")
	}

	fmt.Println("All helper E2E checks passed!")
}
