package main

import (
	"strings"
	"testing"
)

func vf(w, h float64, vcodec string) format {
	return format{Width: &w, Height: &h, Vcodec: vcodec, Acodec: "none"}
}

// A vertical video is named by its short side (1080×1920 is "1080p"); the format filter still
// uses the real height, so the same file is picked as before.
func TestOptionLabelsVertical(t *testing.T) {
	fs := []format{vf(1080, 1920, "avc1"), vf(608, 1080, "vp9"), vf(360, 640, "avc1"), vf(1920, 1080, "avc1")}
	opts := buildOptions(fs, true)
	got := map[string]string{}
	for _, o := range opts {
		got[o.Format] = o.Label
	}
	want := map[string]string{
		"bv*[height<=1920]+ba/b[height<=1920]": "1080p MP4",
		"bv*[height<=640]+ba/b[height<=640]":   "360p MP4",
	}
	for f, l := range want {
		if got[f] != l {
			t.Errorf("%s: label %q, want %q", f, got[f], l)
		}
	}
	// Height 1080 has both a landscape 1920×1080 and a vertical 608×1080: the wider one names it.
	if l := got["bv*[height<=1080]+ba/b[height<=1080]"]; !strings.HasPrefix(l, "1080p") {
		t.Errorf("landscape 1080: label %q", l)
	}
}

func TestOptionLabelsLandscape(t *testing.T) {
	opts := buildOptions([]format{vf(1280, 720, "avc1"), vf(640, 360, "avc1")}, true)
	if opts[1].Label != "720p MP4" || opts[2].Label != "360p MP4" {
		t.Errorf("labels: %q, %q", opts[1].Label, opts[2].Label)
	}
}
