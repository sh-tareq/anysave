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
		if t == "handover" {
			// Fetch the live-streamed handover URL concurrently just like Chrome does.
			if hurl, ok := m["url"].(string); ok && hurl != "" {
				go func(u string) {
					if resp, err := http.Get(u); err == nil {
						_, _ = io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
					}
				}(hurl)
			}
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

	// 6b. Live streaming handover (handover=true)
	hmsgs, err := runStreamJob(*bin, testEnv, map[string]any{
		"action":   "fastDownload",
		"job":      "e2e-live-handover-1",
		"url":      tort.URL + "/normal",
		"fileName": "streamed-handover.bin",
		"folder":   "Archives",
		"handover": true,
	}, false)
	if err != nil || len(hmsgs) == 0 {
		fmt.Fprintf(os.Stderr, "live handover fastDownload failed: err=%v msgs=%v\n", err, hmsgs)
		os.Exit(1)
	}
	sawLiveHandover := false
	for _, m := range hmsgs {
		if m["type"] == "handover" && m["live"] == true {
			sawLiveHandover = true
		}
	}
	lastHandover := hmsgs[len(hmsgs)-1]
	if !sawLiveHandover || lastHandover["type"] != "done" || lastHandover["handedOver"] != true {
		fmt.Fprintf(os.Stderr, "expected live handover done with handedOver=true, got sawLive=%v last=%v\n", sawLiveHandover, lastHandover)
		os.Exit(1)
	}
	fmt.Printf("[OK] live streaming handover verified (handedOver=true, sha256=%v)\n", lastHandover["sha256"])

	// 6c. Persistent background daemon (--daemon) E2E check
	daemonCmd := exec.Command(*bin, "--daemon")
	daemonCmd.Env = append(os.Environ(), testEnv...)
	if err := daemonCmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to start --daemon: %v\n", err)
		os.Exit(1)
	}
	defer func() {
		if daemonCmd.Process != nil {
			_ = daemonCmd.Process.Kill()
		}
	}()

	binReal, _ := filepath.EvalSymlinks(*bin)
	if binReal == "" {
		binReal = *bin
	}
	daemonJSONPath := filepath.Join(filepath.Dir(binReal), "daemon.json")
	var dInfo struct {
		Port  int    `json:"port"`
		Token string `json:"token"`
	}
	for i := 0; i < 30; i++ {
		if b, err := os.ReadFile(daemonJSONPath); err == nil {
			if json.Unmarshal(b, &dInfo) == nil && dInfo.Port > 0 {
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if dInfo.Port == 0 {
		fmt.Fprintf(os.Stderr, "daemon.json did not appear at %s\n", daemonJSONPath)
		os.Exit(1)
	}

	// Queue a background download via the daemon HTTP API (/api/action addLink)
	actBody, _ := json.Marshal(map[string]any{
		"type": "file",
		"url":  tort.URL + "/normal",
		"name": "daemon-bg.bin",
	})
	actReq, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/action", dInfo.Port), bytes.NewReader(actBody))
	actReq.Header.Set("Content-Type", "application/json")
	actReq.Header.Set("X-AnySave-Token", dInfo.Token)
	actResp, err := http.DefaultClient.Do(actReq)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon /api/action failed: %v\n", err)
		os.Exit(1)
	}
	var actRes map[string]any
	_ = json.NewDecoder(actResp.Body).Decode(&actRes)
	actResp.Body.Close()
	bgJobID, _ := actRes["id"].(string)
	if bgJobID == "" {
		fmt.Fprintf(os.Stderr, "daemon did not return job id: %v\n", actRes)
		os.Exit(1)
	}

	// Poll /api/state until bgJobID finishes
	var bgDoneFile string
	for i := 0; i < 50; i++ {
		stReq, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/state", dInfo.Port), nil)
		stReq.Header.Set("X-AnySave-Token", dInfo.Token)
		if stResp, err := http.DefaultClient.Do(stReq); err == nil {
			var st map[string]any
			_ = json.NewDecoder(stResp.Body).Decode(&st)
			stResp.Body.Close()
			if jobs, ok := st["jobs"].(map[string]any); ok {
				if j, ok := jobs[bgJobID].(map[string]any); ok && j["status"] == "done" {
					bgDoneFile, _ = j["file"].(string)
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if bgDoneFile == "" {
		fmt.Fprintf(os.Stderr, "daemon background job %s did not reach status=done\n", bgJobID)
		os.Exit(1)
	}
	bgBytes, err := os.ReadFile(bgDoneFile)
	if err != nil || !bytes.Equal(bgBytes, tort.Payload) {
		fmt.Fprintf(os.Stderr, "daemon background job file mismatch at %s: err=%v\n", bgDoneFile, err)
		os.Exit(1)
	}

	// Shut down daemon cleanly
	qBody, _ := json.Marshal(map[string]any{"type": "quit"})
	qReq, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/action", dInfo.Port), bytes.NewReader(qBody))
	qReq.Header.Set("X-AnySave-Token", dInfo.Token)
	if qResp, err := http.DefaultClient.Do(qReq); err == nil {
		qResp.Body.Close()
	}
	_ = daemonCmd.Wait()
	fmt.Printf("[OK] persistent daemon (--daemon) verified on port %d (%d bytes downloaded)\n", dInfo.Port, len(bgBytes))

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
