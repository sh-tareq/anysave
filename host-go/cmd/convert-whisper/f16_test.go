package main

import (
	"encoding/binary"
	"encoding/hex"
	"testing"
)

func TestF16(t *testing.T) {
	vals := []float32{0, 1, -2.5, 65504, 1e-5, 6.1e-5, 3.14159, 1e10, -1e-8, 0.333333}
	// Python struct.pack('<e', v) for the same values (1e10 -> inf).
	want := "0000 003c 00c1 ff7b a800 ff03 4842 007c 0080 5535"
	got := ""
	for i, v := range vals {
		b := make([]byte, 2)
		binary.LittleEndian.PutUint16(b, f16(v))
		if i > 0 {
			got += " "
		}
		got += hex.EncodeToString(b)
	}
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}
