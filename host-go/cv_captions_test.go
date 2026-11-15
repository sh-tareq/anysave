package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWhisperWords(t *testing.T) {
	// "আমি ভালো আছি।" with the first letter's bytes split over two tokens, as whisper does for Bangla.
	ami := []byte("আমি")
	json := `{"transcription":[{"tokens":[` +
		`{"text":"[_BEG_]","offsets":{"from":0,"to":0}},` +
		`{"text":" ` + string(ami[:2]) + `","offsets":{"from":100,"to":200}},` +
		`{"text":"` + string(ami[2:]) + `","offsets":{"from":200,"to":400}},` +
		`{"text":" ভালো","offsets":{"from":500,"to":900}},` +
		`{"text":" আছি","offsets":{"from":950,"to":1300}},` +
		`{"text":"।","offsets":{"from":1300,"to":1350}},` +
		`{"text":" \"Hi\"","offsets":{"from":1400,"to":1600}}` +
		`]}]}`
	p := filepath.Join(t.TempDir(), "w.json")
	os.WriteFile(p, []byte(json), 0o644)
	w := whisperWords(p, 10)
	want := []capWord{{"আমি", 10.1, 10.4}, {"ভালো", 10.5, 10.9}, {"আছি।", 10.95, 11.35}, {`"Hi"`, 11.4, 11.6}}
	if len(w) != len(want) {
		t.Fatalf("got %d words: %+v", len(w), w)
	}
	for i := range want {
		if w[i].T != want[i].T || abs(w[i].S-want[i].S) > 1e-9 || abs(w[i].E-want[i].E) > 1e-9 {
			t.Errorf("word %d: got %+v, want %+v", i, w[i], want[i])
		}
	}
}

func TestLoadCaptionsMatchesWords(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	srt := filepath.Join(dir, "v.en.srt")
	os.WriteFile(srt, []byte("1\n00:00:00,000 --> 00:00:01,000\nHello world.\n\n2\n00:00:01,000 --> 00:00:02,000\nEdited line\n"), 0o644)
	saveCaptionWords(srt, []capWord{{"Hello", 0.1, 0.4}, {"world.", 0.5, 0.9}, {"Original", 1.1, 1.5}, {"line", 1.5, 1.9}})
	cues, err := loadCaptions(srt)
	if err != nil {
		t.Fatal(err)
	}
	if len(cues) != 2 || len(cues[0].Words) != 2 || len(cues[1].Words) != 0 {
		t.Fatalf("got %+v", cues)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
