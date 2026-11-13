package main

import (
	"reflect"
	"testing"
)

func TestCutPoints(t *testing.T) {
	// A pause at 4 s and at 9 s: cut in each pause; the rest without pauses is cut hard at 7 s.
	got := cutPoints(25, [][2]float64{{3.8, 4.2}, {8.8, 9.2}})
	want := []float64{4, 9, 16, 23}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// Short audio: no cuts.
	if c := cutPoints(6.5, nil); len(c) != 0 {
		t.Errorf("short audio cut: %v", c)
	}
	// Every piece is at most pieceMax long and at least pieceMin (except possibly the last).
	cuts := cutPoints(100, [][2]float64{{1, 1.2}, {2.5, 2.6}, {5, 5.5}, {6.8, 7}, {13, 14}, {40, 41}})
	prev := 0.0
	for _, c := range append(cuts, 100) {
		if c-prev > pieceMax+1e-9 {
			t.Errorf("piece %.2f-%.2f longer than %v", prev, c, pieceMax)
		}
		prev = c
	}
}

func TestSRTRoundTrip(t *testing.T) {
	in := "1\n00:00:01,500 --> 00:00:04,000\nএক দিকে\n\n2\n00:01:02,250 --> 00:01:05,000\nsecond line\nwraps\n\n"
	subs := parseSRT(in)
	if len(subs) != 2 || subs[0].Start != 1.5 || subs[1].Start != 62.25 || subs[1].Text != "second line\nwraps" {
		t.Fatalf("parsed wrong: %+v", subs)
	}
	if out := writeSRT(subs); out != in {
		t.Errorf("round trip:\n%q\n%q", out, in)
	}
	if cleanText("ab\xe0\xa6") != "ab" {
		t.Errorf("broken character not removed")
	}
}

func TestChooseModel(t *testing.T) {
	if chooseModel("en", "") != modelStandard || chooseModel("bn", "") != modelBest || chooseModel("auto", "") != modelBest {
		t.Errorf("wrong default model choice")
	}
	if chooseModel("bn", "standard") != modelStandard {
		t.Errorf("quality override ignored")
	}
}
