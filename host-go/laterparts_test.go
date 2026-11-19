package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaterParts(t *testing.T) {
	d := t.TempDir()
	for _, n := range []string{"a.zip.001", "a.zip.002", "a.zip.003", "b.txt", "c.1"} {
		os.WriteFile(filepath.Join(d, n), nil, 0o644)
	}
	if got := laterParts(filepath.Join(d, "a.zip.001")); len(got) != 2 || filepath.Base(got[1]) != "a.zip.003" {
		t.Fatalf("parts: %v", got)
	}
	for _, n := range []string{"b.txt", "c.1", "a.zip.002"} {
		if got := laterParts(filepath.Join(d, n)); len(got) != 0 {
			t.Fatalf("%s: %v", n, got)
		}
	}
}
