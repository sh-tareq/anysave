package main

import (
	"testing"
	"time"
)

func TestInWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 11, 13, h, m, 0, 0, time.Local) }
	cases := []struct {
		h, m        int
		start, stop string
		want        bool
	}{
		{0, 59, "01:00", "07:00", false},
		{1, 0, "01:00", "07:00", true},
		{6, 59, "01:00", "07:00", true},
		{7, 0, "01:00", "07:00", false},
		{23, 30, "22:00", "06:00", true}, // crosses midnight
		{3, 0, "22:00", "06:00", true},
		{6, 0, "22:00", "06:00", false},
		{12, 0, "22:00", "06:00", false},
		{12, 0, "08:00", "08:00", true}, // same time: always open
		{12, 0, "", "07:00", true},      // unreadable: never blocks
		{12, 0, "25:00", "07:00", true},
	}
	for _, c := range cases {
		if got := inWindow(at(c.h, c.m), c.start, c.stop); got != c.want {
			t.Errorf("%02d:%02d in %s-%s: got %v, want %v", c.h, c.m, c.start, c.stop, got, c.want)
		}
	}
}
