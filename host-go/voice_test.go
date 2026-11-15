package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSplitText(t *testing.T) {
	text := "First sentence. Second one!\n\nA new paragraph? Yes.\r\n\r\nআমি ভালো আছি। তুমি কেমন আছ?"
	p := splitText(text, 600)
	if len(p) != 3 {
		t.Fatalf("want 3 pieces (one per paragraph), got %d: %+v", len(p), p)
	}
	if p[0].Text != "First sentence. Second one!" || p[0].Pause < 0.5 {
		t.Errorf("piece 0 = %+v", p[0])
	}
	if !strings.HasPrefix(p[2].Text, "আমি") {
		t.Errorf("piece 2 = %+v", p[2])
	}
	long := strings.Repeat("This is a sentence that goes on. ", 60)
	for _, q := range splitText(long, 600) {
		if len(q.Text) > 600 {
			t.Errorf("piece too long: %d", len(q.Text))
		}
	}
	// A long run with no sentence end is still cut, and never inside a letter.
	run := strings.Repeat("আমিভালোআছি", 100)
	var joined string
	for _, q := range splitText(run, 600) {
		if len(q.Text) > 600 || !utf8.ValidString(q.Text) {
			t.Errorf("bad cut: %d bytes, valid=%v", len(q.Text), utf8.ValidString(q.Text))
		}
		joined += q.Text
	}
	if joined != run {
		t.Errorf("text lost when cutting")
	}
	for _, q := range splitText(strings.Repeat("একটি, দুটি, ", 80), 600) {
		if !utf8.ValidString(q.Text) {
			t.Errorf("cut inside a letter at a comma")
		}
	}
	if speakable("--help me") != "help me" {
		t.Errorf("speakable")
	}
}

func TestWavRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.wav")
	pcm := []byte{1, 0, 2, 0, 3, 0}
	if err := writeWav(p, 22050, pcm); err != nil {
		t.Fatal(err)
	}
	r, got, err := readWav(p)
	if err != nil || r != 22050 || string(got) != string(pcm) {
		t.Fatalf("got %d %v %v", r, got, err)
	}
}

func TestVoiceList(t *testing.T) {
	vs := ttsVoices()
	langs := map[string]int{}
	for _, v := range vs {
		langs[v.Lang]++
		if v.Lang == "" || v.Name == "" {
			t.Errorf("voice without a name: %+v", v)
		}
	}
	if langs["English (US)"] != 20 || langs["Bangla"] != 16 || langs["Japanese"] != 0 || langs["Spanish"] != 3 {
		t.Errorf("languages: %v", langs)
	}
	if v, ok := voiceByID("k3"); !ok || v.Name != "Heart" || v.Gender != "woman" {
		t.Errorf("k3 = %+v", v)
	}
}

// TestVoiceLabForReal downloads the engine and models and runs them. It's slow and needs the internet:
// ANYSAVE_VOICE_TEST=1 HOME=/some/scratch/home go test -run VoiceLabForReal -v
func TestVoiceLabForReal(t *testing.T) {
	if os.Getenv("ANYSAVE_VOICE_TEST") == "" {
		t.Skip("set ANYSAVE_VOICE_TEST=1")
	}
	out := t.TempDir()
	if d := os.Getenv("ANYSAVE_VOICE_OUT"); d != "" {
		out = d
	}
	ctx := context.Background()
	stage := func(s string) { t.Log(s) }
	last := -10.0
	progress := func(p float64) {
		if p-last >= 10 {
			last = p
			t.Logf("%.0f%%", p)
		}
	}
	for _, c := range []struct{ voice, text, format string }{
		{"k3", "Hello! This is AnySave reading aloud, on this computer.\n\nA second paragraph, after a pause.", "mp3"},
		{"bn0", "আমার সোনার বাংলা, আমি তোমায় ভালোবাসি।", "wav"},
		{"k28", "Hola, me llamo Dora.", "m4a"},
	} {
		last = -10
		res, err := cvRunStaged(ctx, nil, cvSpec{Tool: cvSpeak, Format: c.format, Opts: map[string]string{"voice": c.voice, "text": c.text}}, out, progress, stage)
		if err != nil {
			t.Fatalf("speak %s: %v", c.voice, err)
		}
		mi := probeMedia(res)
		t.Logf("%s -> %s (%.1fs)", c.voice, filepath.Base(res), mi.Duration)
		if mi.Duration < 1 {
			t.Errorf("too short")
		}
	}
	noisy := os.Getenv("ANYSAVE_VOICE_NOISY")
	if noisy == "" {
		t.Log("no ANYSAVE_VOICE_NOISY: skipping clean-up")
		return
	}
	for _, s := range []string{"strong", "light", "basic"} {
		last = -10
		res, err := cvRunStaged(ctx, []string{noisy}, cvSpec{Tool: cvVoiceClean, Opts: map[string]string{"strength": s}}, out, progress, stage)
		if err != nil {
			t.Fatalf("clean %s: %v", s, err)
		}
		t.Logf("%s -> %s (%.1fs)", s, filepath.Base(res), probeMedia(res).Duration)
	}
}
