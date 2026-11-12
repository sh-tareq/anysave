package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignAndVerifyManifest(t *testing.T) {
	pub, priv, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}

	m := Manifest{
		Channel: ChannelStable,
		Version: "1.0.6",
		Notes:   "Fast multi-connection engine",
		Assets: map[string]Asset{
			"darwin-universal": {
				URL:    "https://github.com/sh-tareq/anysave/releases/download/v1.0.6/AnySave-Desktop.zip",
				SHA256: strings.Repeat("a", 64),
				Size:   1024,
			},
		},
	}

	signed, err := Sign(m, priv)
	if err != nil {
		t.Fatalf("Sign failed: %v", err)
	}

	// 1. Valid verification
	got, err := Verify(signed, pub, ChannelStable, "1.0.5")
	if err != nil {
		t.Fatalf("Verify valid manifest failed: %v", err)
	}
	if got.Version != "1.0.6" || got.Channel != ChannelStable {
		t.Fatalf("unexpected manifest: %+v", got)
	}

	// 2. Tampered payload (change URL or SHA256 after signing)
	tampered := strings.Replace(string(signed), "v1.0.6", "v1.0.9", 1)
	if _, err := Verify([]byte(tampered), pub, ChannelStable, "1.0.5"); err == nil {
		t.Fatal("expected tampered manifest to fail signature verification")
	}

	// 3. Wrong public key
	otherPub, _, _ := GenerateKeypair()
	if _, err := Verify(signed, otherPub, ChannelStable, "1.0.5"); err == nil {
		t.Fatal("expected wrong public key to fail verification")
	}

	// 4. Channel mismatch (beta manifest presented on stable channel)
	if _, err := Verify(signed, pub, ChannelBeta, "1.0.5"); err == nil {
		t.Fatal("expected channel mismatch to fail")
	}

	// 5. Rollback / downgrade attack (manifest is 1.0.6, current installed is 1.0.7)
	if _, err := Verify(signed, pub, ChannelStable, "1.0.7"); err == nil {
		t.Fatal("expected rollback version to be rejected")
	}
}

func TestCompareVersions(t *testing.T) {
	if CompareVersions("1.0.6", "1.0.5") <= 0 {
		t.Error("expected 1.0.6 > 1.0.5")
	}
	if CompareVersions("1.1.0", "1.1.0-beta.1") <= 0 {
		t.Error("expected 1.1.0 > 1.1.0-beta.1")
	}
	if CompareVersions("1.1.0-beta.2", "1.1.0-beta.1") <= 0 {
		t.Error("expected 1.1.0-beta.2 > 1.1.0-beta.1")
	}
	if CompareVersions("v1.0.5", "1.0.5") != 0 {
		t.Error("expected v1.0.5 == 1.0.5")
	}
}

func TestVerifyFile(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "payload.bin")
	content := []byte("AnySave verified binary content")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	asset := Asset{
		SHA256: hex.EncodeToString(sum[:]),
		Size:   int64(len(content)),
	}
	if err := VerifyFile(p, asset); err != nil {
		t.Fatalf("VerifyFile failed on valid file: %v", err)
	}
	if err := VerifyFile(p, Asset{SHA256: strings.Repeat("0", 64), Size: int64(len(content))}); err == nil {
		t.Fatal("expected wrong SHA-256 to fail")
	}
	if err := VerifyFile(p, Asset{SHA256: asset.SHA256, Size: 99999}); err == nil {
		t.Fatal("expected wrong file size to fail")
	}
}

