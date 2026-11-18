package main

// Caption timing that follows the voice. Whisper's times are roughly right but a line often
// "starts" in the silence before it (the first line of a video at 0:00 when the talking begins
// at 2.9 s). Here the sound itself says where speech is, and each line and word is moved to
// begin when the speaking does. Only starts that sit in a quiet stretch are moved, so on music
// or noisy sound (where everything looks loud) nothing changes.

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const (
	syncFrame = 0.02 // seconds per loudness measurement
	syncLead  = 0.05 // show a line this much before the first sound of it
)

// speechSpans finds the loud stretches (speech) in 16 kHz mono 16-bit PCM WAV.
// The quiet level is learned from the sound itself, so it works for quiet and loud recordings.
func speechSpans(wav string) [][2]float64 {
	f, err := os.Open(wav)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || len(b) < 44 {
		return nil
	}
	// Find the "data" chunk (ffmpeg may add a LIST chunk before it).
	data := b[12:]
	var pcm []byte
	for len(data) >= 8 {
		id, n := string(data[:4]), int(binary.LittleEndian.Uint32(data[4:8]))
		data = data[8:]
		if n > len(data) || n < 0 {
			n = len(data)
		}
		if id == "data" {
			pcm = data[:n]
			break
		}
		data = data[n+n%2:]
	}
	const rate = 16000
	per := int(syncFrame * rate)
	frames := len(pcm) / 2 / per
	if frames < 10 {
		return nil
	}
	db := make([]float64, frames)
	for i := 0; i < frames; i++ {
		var sum float64
		for k := 0; k < per; k++ {
			v := float64(int16(binary.LittleEndian.Uint16(pcm[(i*per+k)*2:]))) / 32768
			sum += v * v
		}
		db[i] = 10 * math.Log10(sum/float64(per)+1e-10)
	}
	sorted := append([]float64(nil), db...)
	sort.Float64s(sorted)
	floor, loud := sorted[frames/10], sorted[frames*95/100]
	if loud-floor < 12 { // no clear quiet parts (music, steady noise): don't guess
		return nil
	}
	thr := floor + math.Max(8, (loud-floor)*0.3)
	var spans [][2]float64
	start := -1
	for i := 0; i <= frames; i++ {
		on := i < frames && db[i] >= thr
		if on && start < 0 {
			start = i
		} else if !on && start >= 0 {
			spans = append(spans, [2]float64{float64(start) * syncFrame, float64(i) * syncFrame})
			start = -1
		}
	}
	// Join speech split by tiny gaps (between syllables), drop clicks (under 0.1 s).
	var out [][2]float64
	for _, s := range spans {
		if len(out) > 0 && s[0]-out[len(out)-1][1] < 0.15 {
			out[len(out)-1][1] = s[1]
			continue
		}
		out = append(out, s)
	}
	keep := out[:0]
	for _, s := range out {
		if s[1]-s[0] >= 0.1 {
			keep = append(keep, s)
		}
	}
	return keep
}

// speechAfter returns where speech next begins at or after t (and before limit), or -1.
// If t is already inside speech it returns t.
func speechAfter(spans [][2]float64, t, limit float64) float64 {
	for _, s := range spans {
		if s[1] <= t {
			continue
		}
		if s[0] <= t {
			return t
		}
		if s[0] < limit {
			return s[0]
		}
		return -1
	}
	return -1
}

// syncStart moves a start that lies in a quiet stretch to just before the next speech
// (as long as that speech still belongs to the line, i.e. starts before end).
func syncStart(spans [][2]float64, s, e float64) float64 {
	on := speechAfter(spans, s, e-0.04)
	if on <= s {
		return s
	}
	return math.Max(s, on-syncLead)
}

// syncEnd ends a line half a second after its last sound, when it would otherwise stay on
// screen through a long pause.
func syncEnd(spans [][2]float64, s, e float64) float64 {
	last := -1.0
	for _, sp := range spans {
		if sp[0] >= e {
			break
		}
		if sp[1] > s {
			last = math.Min(sp[1], e)
		}
	}
	if last > s && e-last > 0.7 {
		return last + 0.5
	}
	return e
}

// syncCaptions moves line and word starts to the speech. Returns how many lines moved.
func syncCaptions(spans [][2]float64, subs []subtitle, words []capWord) int {
	if len(spans) == 0 {
		return 0
	}
	for i := range words {
		words[i].S = syncStart(spans, words[i].S, words[i].E)
	}
	moved := 0
	for i := range subs {
		ns := syncStart(spans, subs[i].Start, subs[i].End)
		if ns > subs[i].Start+0.15 {
			moved++
		}
		subs[i].Start = ns
		subs[i].End = syncEnd(spans, ns, subs[i].End)
	}
	return moved
}

// captionSync answers the page's "Sync to speech": the speech in the video, for the page to
// move its lines with (the page holds the edited lines, so it does the moving).
func (s *cvServer) captionSync(ctx context.Context, video string) map[string]any {
	ff, err := ffmpegPath()
	if err != nil {
		return fail(errMsg(err))
	}
	wav := filepath.Join(s.tmp, "sync-"+time.Now().Format("150405.000")+".wav")
	defer os.Remove(wav)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if out, err := commandContext(ctx, ff, "-hide_banner", "-loglevel", "error", "-y", "-i", video, "-vn", "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav).CombinedOutput(); err != nil {
		logf("caption sync: %s", out)
		return fail("Couldn't read the sound of this video.")
	}
	spans := speechSpans(wav)
	if spans == nil {
		return fail("The speech can't be told apart from the background here (music or noise all the way through), so the timing was left as it is.")
	}
	return map[string]any{"ok": true, "speech": spans}
}
