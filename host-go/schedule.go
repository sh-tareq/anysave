package main

// Scheduler and queues for the desktop service.
//
// Settings:
//
//	scheduleOn     true = only download inside the time window
//	scheduleStart  "01:00"   scheduleStop  "07:00"   (the window may cross midnight)
//	scheduleQueue  ""  = the window applies to every download; a queue name = only to that queue
//
// A download that has to wait gets the status "scheduled". When the window opens, everything
// scheduled starts; when it closes, running downloads are paused and wait for the next window.
// "Start now" on a row (ignoreSchedule) lets one download run anyway.

import (
	"strconv"
	"strings"
	"time"
)

// clockMinutes turns "07:30" into 450. ok is false for anything else.
func clockMinutes(s string) (int, bool) {
	h, m, found := strings.Cut(strings.TrimSpace(s), ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	if !found || err1 != nil || err2 != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}

// inWindow reports whether now is inside start..stop (clock times; stop is not included).
// A window with the same start and stop, or unreadable times, is always open.
func inWindow(now time.Time, start, stop string) bool {
	s, ok1 := clockMinutes(start)
	e, ok2 := clockMinutes(stop)
	if !ok1 || !ok2 || s == e {
		return true
	}
	t := now.Hour()*60 + now.Minute()
	if s < e {
		return t >= s && t < e
	}
	return t >= s || t < e // crosses midnight
}

// scheduleNow is the clock the scheduler uses (replaced in tests).
var scheduleNow = time.Now

// blockedLocked reports whether the schedule makes this job wait right now. Call with d.mu held.
func (d *daemonServer) blockedLocked(j map[string]any) bool {
	on, _ := d.settings["scheduleOn"].(bool)
	if !on || j == nil {
		return false
	}
	if ignore, _ := j["ignoreSchedule"].(bool); ignore {
		return false
	}
	if q, _ := d.settings["scheduleQueue"].(string); q != "" && q != str(j, "queue") {
		return false
	}
	start, _ := d.settings["scheduleStart"].(string)
	stop, _ := d.settings["scheduleStop"].(string)
	return !inWindow(scheduleNow(), start, stop)
}

// holdForSchedule is called when a download is about to start. If it has to wait for the
// time window it is marked "scheduled" and true is returned (the caller must not start it).
func (d *daemonServer) holdForSchedule(id string) bool {
	d.mu.Lock()
	blocked := d.blockedLocked(d.jobs[id])
	d.mu.Unlock()
	if blocked {
		d.updateJob(id, map[string]any{"status": "scheduled", "speed": "", "eta": ""})
	}
	return blocked
}

// scheduleTick starts what may run now and pauses what may not.
func (d *daemonServer) scheduleTick() {
	var release, hold []string
	d.mu.Lock()
	for id, j := range d.jobs {
		kind := str(j, "kind")
		if kind != "file" && kind != "video" {
			continue
		}
		blocked := d.blockedLocked(j)
		switch str(j, "status") {
		case "scheduled":
			if !blocked {
				release = append(release, id)
			}
		case "starting", "downloading":
			if blocked {
				hold = append(hold, id)
			}
		}
	}
	d.mu.Unlock()
	for _, id := range hold {
		d.updateJob(id, map[string]any{"heldBySchedule": true})
		if !d.sendWorkerControl(id, "pause") {
			d.updateJob(id, map[string]any{"heldBySchedule": false})
			continue
		}
		d.updateJob(id, map[string]any{"status": "scheduled", "speed": "", "eta": ""})
	}
	for _, id := range release {
		d.updateJob(id, map[string]any{"heldBySchedule": false})
		d.resumeJob(id)
	}
}

func (d *daemonServer) scheduleLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-d.quitCh:
			return
		case <-t.C:
			d.scheduleTick()
		}
	}
}

// jobInQueue makes sure a new download knows its queue before it starts (so a schedule for
// that queue applies from the first moment). It returns the id to start the download with.
func (d *daemonServer) jobInQueue(id, queue string) string {
	queue = strings.TrimSpace(queue)
	if queue == "" {
		return id
	}
	if id == "" {
		id = newDaemonJobID()
	}
	d.updateJob(id, map[string]any{"queue": queue})
	return id
}
