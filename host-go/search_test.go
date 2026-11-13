package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSearchTranscripts(t *testing.T) {
	root := t.TempDir()
	vids := filepath.Join(root, "Videos")
	os.MkdirAll(vids, 0o755)
	os.MkdirAll(filepath.Join(root, ".anysave-parts", "x"), 0o755)
	write := func(p, s string) {
		t.Helper()
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(vids, "Ad one.en.srt"), "1\n00:00:01,000 --> 00:00:03,000\nBuy our new running shoes\n\n2\n00:01:05,500 --> 00:01:08,000\nRunning has never been\n\n3\n00:01:08,000 --> 00:01:10,000\nthis comfortable. Shoes!\n\n")
	write(filepath.Join(vids, "Ad one.en.txt"), "Buy our new running shoes ...")
	write(filepath.Join(vids, "Ad one.mp4"), "video")
	write(filepath.Join(vids, "খবর.bn.srt"), "1\n00:00:02,000 --> 00:00:05,000\nএক দিকে কার্যক্রম নিষিদ্ধ\n\n2\n00:00:05,000 --> 00:00:08,000\nআওয়ামী লীগ মাঠে\n\n")
	write(filepath.Join(vids, "notes.srt"), "1\n00:00:01,000 --> 00:00:02,000\nshoes\n\n")                         // not a transcript name
	write(filepath.Join(root, ".anysave-parts", "x", "tmp.en.srt"), "1\n00:00:01,000 --> 00:00:02,000\nshoes\n\n") // unfinished pieces

	// Library view: both transcripts, nothing else.
	res, scanned := searchTranscripts("", []string{root})
	if scanned != 2 || len(res) != 2 {
		t.Fatalf("library: scanned %d, results %d", scanned, len(res))
	}
	// English, case and punctuation ignored; the video and text file are found.
	res, _ = searchTranscripts("SHOES", []string{root})
	if len(res) != 1 || res[0].Count != 2 || res[0].Video == "" || res[0].TXT == "" || res[0].Lang != "en" {
		t.Fatalf("shoes: %+v", res)
	}
	if res[0].Matches[0].Time != "00:01" || res[0].Matches[1].Time != "01:08" {
		t.Errorf("times: %+v", res[0].Matches)
	}
	// A phrase split across two subtitle lines is still found (at the first line).
	res, _ = searchTranscripts("never been this comfortable", []string{root})
	if len(res) != 1 || res[0].Count != 1 || res[0].Matches[0].Time != "01:05" {
		t.Errorf("split phrase: %+v", res)
	}
	// Bangla, including a word with a zero-width joiner typed differently.
	res, _ = searchTranscripts("আওয়ামী\u200d লীগ", []string{root})
	if len(res) != 1 || res[0].Lang != "bn" || res[0].Matches[0].Time != "00:05" {
		t.Errorf("bangla: %+v", res)
	}
	if res, _ := searchTranscripts("nothing like this", []string{root}); len(res) != 0 {
		t.Errorf("no match expected: %+v", res)
	}
}
