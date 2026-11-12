//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func readClipboardText() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pbpaste").Output()
	if err != nil {
		return ""
	}
	return string(out)
}

func notifyOS(title, msg string) {
	if runtime.GOOS != "darwin" {
		return
	}
	esc := func(s string) string {
		s = strings.ReplaceAll(s, `\`, `\\`)
		return strings.ReplaceAll(s, `"`, `\"`)
	}
	script := fmt.Sprintf(`display notification "%s" with title "%s" sound name "Glass"`, esc(msg), esc(title))
	_ = exec.Command("osascript", "-e", script).Start()
}

func openAppWindow(url string, isDropBox bool) {
	if runtime.GOOS == "darwin" {
		// Prefer chromeless app window when Chrome/Brave/Edge is available for the Drop Box,
		// otherwise open in the default browser (or AnySave.app handles its own WKWebView).
		if isDropBox {
			browsers := []string{
				"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
				"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser",
				"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			}
			for _, b := range browsers {
				if isFile(b) {
					_ = exec.Command(b, "--app="+url, "--window-size=320,210").Start()
					return
				}
			}
		}
		_ = exec.Command("open", url).Start()
		return
	}
	_ = exec.Command("xdg-open", url).Start()
}

func runPlatformApp(baseURL, _ string, d *daemonServer) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-d.quitCh:
	case <-sigCh:
	}
}

