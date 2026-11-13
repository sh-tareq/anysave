package main

// What happens when every download has finished: nothing, show/open the last file, put the
// computer to sleep, shut it down, or quit AnySave. Sleep and shut down wait a minute first
// (with a notification), and are called off if a download starts or the setting changes.

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// powerDelay is how long the warning is shown before the computer sleeps or shuts down.
func powerDelay() time.Duration {
	if ms, err := strconv.Atoi(os.Getenv("ANYSAVE_POWER_DELAY_MS")); err == nil && ms >= 0 {
		return time.Duration(ms) * time.Millisecond // for tests
	}
	return 60 * time.Second
}

// powerAction puts the computer to sleep or shuts it down. With ANYSAVE_DRY_POWER=1 it only
// writes what it would do to the log (for tests).
func powerAction(kind string) error {
	if kind != "sleep" && kind != "shutdown" {
		return fmt.Errorf("unknown power action %q", kind)
	}
	if os.Getenv("ANYSAVE_DRY_POWER") == "1" {
		logf("power (dry run): %s", kind)
		return nil
	}
	logf("power: %s", kind)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		if kind == "sleep" {
			cmd = exec.Command("pmset", "sleepnow")
		} else {
			cmd = exec.Command("osascript", "-e", `tell application "System Events" to shut down`)
		}
	case "windows":
		if kind == "sleep" {
			cmd = command("rundll32.exe", "powrprof.dll,SetSuspendState", "0,1,0")
		} else {
			cmd = command("shutdown", "/s", "/t", "20", "/c", "AnySave: all downloads have finished.")
		}
	default:
		if kind == "sleep" {
			cmd = exec.Command("systemctl", "suspend")
		} else {
			cmd = exec.Command("systemctl", "poweroff")
		}
	}
	return cmd.Run()
}

// actionPower is the helper action the browser extension uses: {"action":"power","do":"sleep"}.
func actionPower(msg map[string]any) map[string]any {
	if err := powerAction(str(msg, "do")); err != nil {
		return map[string]any{"ok": false, "error": err.Error()}
	}
	return map[string]any{"ok": true}
}

func powerWords(kind string) string {
	switch kind {
	case "sleep":
		return "go to sleep"
	case "shutdown":
		return "shut down"
	}
	return "quit AnySave"
}

// busyLocked reports whether any download is still running or waiting. Call with d.mu held.
func (d *daemonServer) busyLocked() bool {
	for _, j := range d.jobs {
		switch str(j, "status") {
		case "starting", "downloading", "queued", "scheduled":
			return true
		}
	}
	return false
}

// afterAllDone runs the "when all downloads finish" action after job id has finished.
func (d *daemonServer) afterAllDone(id string) {
	d.mu.Lock()
	act, _ := d.settings["afterDoneAction"].(string)
	busy := d.busyLocked()
	file := str(d.jobs[id], "file")
	d.powerToken++
	token := d.powerToken
	d.mu.Unlock()
	if busy || act == "" || act == "none" {
		return
	}
	switch act {
	case "reveal":
		if file != "" {
			actionReveal(map[string]any{"file": file})
		}
	case "open":
		if file != "" {
			actionOpen(map[string]any{"file": file})
		}
	case "sleep", "shutdown", "quit":
		delay := powerDelay()
		if act != "quit" {
			notifyOS("AnySave: all downloads finished", fmt.Sprintf(
				"This computer will %s in %d seconds. Start a download or change the setting to cancel.",
				powerWords(act), int(delay.Seconds())))
		} else {
			delay = 0
		}
		time.Sleep(delay)
		d.mu.Lock()
		still, _ := d.settings["afterDoneAction"].(string)
		ok := d.powerToken == token && still == act && !d.busyLocked()
		d.mu.Unlock()
		if !ok {
			logf("after-done %s called off", act)
			return
		}
		if act == "quit" {
			d.requestQuit()
			return
		}
		if err := powerAction(act); err != nil {
			logf("after-done %s failed: %v", act, err)
		}
	}
}

// requestQuit stops the background service; the desktop app sees "quitting" and closes too.
func (d *daemonServer) requestQuit() {
	d.mu.Lock()
	d.quitting = true
	d.mu.Unlock()
	go func() {
		time.Sleep(2500 * time.Millisecond) // long enough for the app's 1-second check to see it
		select {
		case <-d.quitCh:
		default:
			close(d.quitCh)
		}
	}()
}
