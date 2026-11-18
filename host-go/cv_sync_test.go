package main

import (
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// voiceWav makes 16 kHz mono sound: faint noise, with a loud "voice" in the given stretches.
func voiceWav(t *testing.T, secs float64, voice [][2]float64) string {
	n := int(secs * 16000)
	pcm := make([]byte, 44+n*2)
	copy(pcm, "RIFF")
	copy(pcm[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(pcm[16:], 16)
	copy(pcm[36:], "data")
	binary.LittleEndian.PutUint32(pcm[40:], uint32(n*2))
	r := rand.New(rand.NewSource(1))
	for i := 0; i < n; i++ {
		tm := float64(i) / 16000
		v := r.NormFloat64() * 0.002
		for _, s := range voice {
			if tm >= s[0] && tm < s[1] {
				v += 0.3 * math.Sin(2*math.Pi*180*tm) * (0.6 + 0.4*math.Sin(2*math.Pi*4*tm))
			}
		}
		binary.LittleEndian.PutUint16(pcm[44+i*2:], uint16(int16(math.Max(-1, math.Min(1, v))*32767)))
	}
	p := filepath.Join(t.TempDir(), "a.wav")
	os.WriteFile(p, pcm, 0o644)
	return p
}

func TestSyncCaptions(t *testing.T) {
	wav := voiceWav(t, 14, [][2]float64{{2.86, 4.96}, {5.32, 8.5}, {8.86, 9.5}})
	spans := speechSpans(wav)
	if len(spans) != 3 || math.Abs(spans[0][0]-2.86) > 0.05 {
		t.Fatalf("speech found at %v", spans)
	}
	subs := []subtitle{{0, 5.24, "Want to see"}, {5.24, 8.96, "Tap learn more"}, {8.96, 13, "you'll find it."}}
	words := []capWord{{"Want", 0, 3.2}, {"to", 3.2, 3.5}}
	moved := syncCaptions(spans, subs, words)
	if moved < 1 || math.Abs(subs[0].Start-2.81) > 0.05 {
		t.Errorf("first line starts %.2f (moved %d), want about 2.81", subs[0].Start, moved)
	}
	if math.Abs(subs[1].Start-5.27) > 0.03 { // speech at 5.32: shown 0.05 s before it
		t.Errorf("second line starts %.2f, want about 5.27", subs[1].Start)
	}
	if math.Abs(subs[2].End-10.0) > 0.05 { // speech ends 9.5: off screen half a second later, not at 13
		t.Errorf("last line ends %.2f, want 10.0", subs[2].End)
	}
	if math.Abs(words[0].S-2.81) > 0.05 || words[1].S != 3.2 {
		t.Errorf("words %v", words)
	}
}

// Sound all the way through (music, steady noise): a line starting at 0 is not pushed later.
func TestSyncNoQuietParts(t *testing.T) {
	if s := speechSpans(voiceWav(t, 6, nil)); s != nil {
		t.Fatalf("only faint noise: expected no speech, got %v", s)
	}
	subs := []subtitle{{0, 3, "sung line"}}
	syncCaptions(speechSpans(voiceWav(t, 6, [][2]float64{{0, 6}})), subs, nil)
	if subs[0].Start != 0 {
		t.Fatalf("line moved to %.2f", subs[0].Start)
	}
}

// Optional: SYNC_WAV=real.wav SYNC_SRT=real.srt go test -run TestSyncReal -v
func TestSyncReal(t *testing.T) {
	w, srt := os.Getenv("SYNC_WAV"), os.Getenv("SYNC_SRT")
	if w == "" {
		t.Skip()
	}
	b, _ := os.ReadFile(srt)
	subs := parseSRT(string(b))
	before := append([]subtitle(nil), subs...)
	spans := speechSpans(w)
	t.Logf("speech: %v", spans)
	syncCaptions(spans, subs, nil)
	for i := range subs {
		t.Logf("%6.2f-%6.2f  →  %6.2f-%6.2f  %s", before[i].Start, before[i].End, subs[i].Start, subs[i].End, subs[i].Text)
	}
}
