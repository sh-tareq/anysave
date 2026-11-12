package main

// The `anysave` command:
//
//	anysave <link> [more links...]      add downloads to AnySave
//	anysave --wait <link>               also wait until it has finished and print the file
//	anysave --sha256 <hash> <link>      check the finished file against a SHA-256
//	anysave                             open the AnySave window
//
// Links go to the AnySave background service, which is started if it isn't running.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

const cliUsage = `AnySave - download videos and files

Usage:
  anysave <link> [more links...]     add one or more downloads
  anysave --wait <link>              wait until the download has finished, then print the file
  anysave --sha256 <hash> <link>     check the finished file against this SHA-256 checksum
  anysave --version                  print the version
  anysave                            open the AnySave window

Downloads are saved in Downloads/AnySave and show up in the AnySave window.
`

var sha256Re = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

func isLinkArg(a string) bool {
	l := strings.ToLower(a)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "anysave://")
}

// runCLI handles command-line use. handled is false when the arguments aren't for the CLI
// (for example when a browser starts the helper), so the caller carries on as before.
func runCLI(args []string, stdout, stderr io.Writer) (code int, handled bool) {
	var links []string
	wait, sha := false, ""
	for i := 0; i < len(args); i++ {
		a := strings.TrimSpace(args[i])
		switch {
		case a == "-h" || a == "--help" || a == "help":
			fmt.Fprint(stdout, cliUsage)
			return 0, true
		case a == "--wait" || a == "-w":
			wait = true
		case a == "--sha256":
			if i+1 >= len(args) || !sha256Re.MatchString(strings.TrimSpace(args[i+1])) {
				fmt.Fprintln(stderr, "anysave: --sha256 needs a 64-character SHA-256 checksum")
				return 2, true
			}
			i++
			sha = strings.ToLower(strings.TrimSpace(args[i]))
		case isLinkArg(a):
			links = append(links, a)
		default:
			// A browser starts the helper with its extension address; "Send to" passes a file.
			// Those aren't commands: let the caller handle them.
			l := strings.ToLower(a)
			if strings.HasPrefix(l, "chrome-extension://") || strings.HasPrefix(l, "--parent-window") ||
				((strings.HasSuffix(l, ".url") || strings.HasSuffix(l, ".txt")) && isFile(a)) {
				return 0, false
			}
			fmt.Fprintf(stderr, "anysave: %q isn't a web link\n\n%s", a, cliUsage)
			return 2, true
		}
	}
	if len(links) == 0 {
		if wait || sha != "" {
			fmt.Fprint(stderr, "anysave: give a link to download\n\n"+cliUsage)
			return 2, true
		}
		return 0, false
	}
	if sha != "" && len(links) > 1 {
		fmt.Fprintln(stderr, "anysave: --sha256 works with one link at a time")
		return 2, true
	}

	failed := 0
	var ids []string
	for _, link := range links {
		payload := map[string]any{"type": "addLink", "url": cleanLink(link)}
		if sha != "" {
			payload = map[string]any{"type": "file", "url": cleanLink(link), "sha256": sha}
		}
		res, err := daemonAction(payload)
		if err == nil && res["ok"] != true {
			err = fmt.Errorf("%s", strOr(res, "error", "AnySave refused the link"))
		}
		if err != nil {
			failed++
			fmt.Fprintf(stderr, "Failed: %s\n        %v\n", link, err)
			continue
		}
		ids = append(ids, str(res, "id"))
		fmt.Fprintf(stdout, "Added:  %s\n", link)
	}
	if wait {
		for _, id := range ids {
			if !waitForJob(id, stdout, stderr) {
				failed++
			}
		}
	}
	if failed > 0 {
		return 1, true
	}
	return 0, true
}

func strOr(m map[string]any, key, fallback string) string {
	if s := str(m, key); s != "" {
		return s
	}
	return fallback
}

// cleanLink turns anysave://?url=<link> (or anysave://<link>) into the plain link.
func cleanLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(strings.ToLower(raw), "anysave://") {
		return raw
	}
	if u, err := neturl.Parse(raw); err == nil {
		if target := u.Query().Get("url"); target != "" {
			return target
		}
	}
	return strings.TrimPrefix(raw[len("anysave://"):], "//")
}

// runningDaemon returns the background service, starting it when needed.
func runningDaemon() (*daemonInfo, error) {
	info := checkRunningDaemon()
	if info == nil {
		self, _ := os.Executable()
		cmd := command(self, "--daemon")
		_ = cmd.Start()
		for i := 0; i < 40 && info == nil; i++ {
			time.Sleep(150 * time.Millisecond)
			info = checkRunningDaemon()
		}
	}
	if info == nil {
		return nil, fmt.Errorf("couldn't start AnySave. Is it installed?")
	}
	return info, nil
}

// daemonAction sends one action to the background service and returns its answer.
func daemonAction(payload map[string]any) (map[string]any, error) {
	info, err := runningDaemon()
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("POST", fmt.Sprintf("http://127.0.0.1:%d/api/action", info.Port), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AnySave-Token", info.Token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("AnySave gave an answer that couldn't be read (HTTP %d)", resp.StatusCode)
	}
	return out, nil
}

// waitForJob follows one download until it ends. It prints the saved file on success.
func waitForJob(id string, stdout, stderr io.Writer) bool {
	lastPct := -10.0
	for {
		info := checkRunningDaemon()
		if info == nil {
			fmt.Fprintln(stderr, "AnySave stopped before the download finished.")
			return false
		}
		req, _ := http.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:%d/api/state", info.Port), nil)
		req.Header.Set("X-AnySave-Token", info.Token)
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err == nil {
			var st struct {
				Jobs map[string]map[string]any `json:"jobs"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&st)
			resp.Body.Close()
			j := st.Jobs[id]
			switch str(j, "status") {
			case "done":
				fmt.Fprintf(stdout, "Saved:  %s\n", str(j, "file"))
				return true
			case "error", "cancelled":
				fmt.Fprintf(stderr, "Failed: %s\n        %s\n", str(j, "url"), strOr(j, "error", str(j, "status")))
				return false
			}
			if pct, _ := j["percent"].(float64); pct >= lastPct+10 {
				lastPct = pct
				fmt.Fprintf(stderr, "  %3.0f%%  %s\n", pct, str(j, "speed"))
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
}
