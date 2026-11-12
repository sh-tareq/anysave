// Package updater verifies Ed25519-signed release manifests and downloaded update
// artifacts across AnySave release channels ("stable" and "beta"), with rollback
// (downgrade) protection and SHA-256 verification.
package updater

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Channel identifies a release track.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// Asset describes a single downloadable binary or archive in a release.
type Asset struct {
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest is the canonical payload signed by the publisher's Ed25519 private key.
type Manifest struct {
	Channel     Channel          `json:"channel"`
	Version     string           `json:"version"`
	PublishedAt string           `json:"publishedAt"`
	MinVersion  string           `json:"minVersion,omitempty"` // optional minimum version required to upgrade directly
	Notes       string           `json:"notes,omitempty"`
	Assets      map[string]Asset `json:"assets"` // e.g. "darwin-universal", "windows-amd64", "android-arm64"
}

// SignedManifest wraps the canonical JSON of a Manifest and its Ed25519 signature.
type SignedManifest struct {
	Payload   Manifest `json:"payload"`
	Signature string   `json:"signature"` // base64-encoded Ed25519 signature over canonical JSON(Payload)
}

// GenerateKeypair creates a new Ed25519 public/private keypair encoded in base64.
func GenerateKeypair() (pubB64, privB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// Sign signs a Manifest with a base64-encoded Ed25519 private key.
func Sign(m Manifest, privKeyB64 string) ([]byte, error) {
	privBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privKeyB64))
	if err != nil || len(privBytes) != ed25519.PrivateKeySize {
		return nil, errors.New("invalid Ed25519 private key")
	}
	if m.Channel != ChannelStable && m.Channel != ChannelBeta {
		return nil, fmt.Errorf("invalid channel %q (must be stable or beta)", m.Channel)
	}
	if m.Version == "" {
		return nil, errors.New("manifest version is required")
	}
	if m.PublishedAt == "" {
		m.PublishedAt = time.Now().UTC().Format(time.RFC3339)
	}
	canonical, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	sig := ed25519.Sign(ed25519.PrivateKey(privBytes), canonical)
	env := SignedManifest{
		Payload:   m,
		Signature: base64.StdEncoding.EncodeToString(sig),
	}
	return json.MarshalIndent(env, "", "  ")
}

// Verify checks the Ed25519 signature on data using pubKeyB64, confirms the manifest
// matches expectedChannel (unless expectedChannel is empty), and rejects downgrades
// below currentVersion (rollback attack protection).
func Verify(data []byte, pubKeyB64 string, expectedChannel Channel, currentVersion string) (*Manifest, error) {
	pubBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubKeyB64))
	if err != nil || len(pubBytes) != ed25519.PublicKeySize {
		return nil, errors.New("invalid Ed25519 public key")
	}
	var env SignedManifest
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("malformed signed manifest: %w", err)
	}
	if env.Payload.Version == "" || env.Signature == "" {
		return nil, errors.New("signed manifest missing payload or signature")
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(env.Signature))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return nil, errors.New("malformed signature")
	}
	canonical, err := json.Marshal(env.Payload)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), canonical, sigBytes) {
		return nil, errors.New("signature verification failed: manifest may have been tampered with")
	}
	m := env.Payload
	if expectedChannel != "" && m.Channel != expectedChannel {
		return nil, fmt.Errorf("channel mismatch: manifest is for %q, expected %q", m.Channel, expectedChannel)
	}
	if currentVersion != "" && CompareVersions(m.Version, currentVersion) < 0 {
		return nil, fmt.Errorf("rollback rejected: manifest version %s is older than current version %s", m.Version, currentVersion)
	}
	return &m, nil
}

// VerifyFile checks that path matches the Asset's size and SHA-256 hash.
func VerifyFile(path string, a Asset) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if a.Size > 0 && st.Size() != a.Size {
		return fmt.Errorf("file size mismatch: got %d bytes, expected %d", st.Size(), a.Size)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	want := strings.ToLower(strings.TrimSpace(a.SHA256))
	if want == "" || got != want {
		return fmt.Errorf("SHA-256 mismatch: got %s, expected %s", got, want)
	}
	return nil
}

// CompareVersions compares two semantic version strings (e.g. "1.0.5", "1.1.0-beta.1").
// Returns -1 if a < b, 0 if a == b, +1 if a > b.
func CompareVersions(a, b string) int {
	aMain, aPre := splitPre(strings.TrimPrefix(strings.TrimSpace(a), "v"))
	bMain, bPre := splitPre(strings.TrimPrefix(strings.TrimSpace(b), "v"))
	aParts := parseNums(aMain)
	bParts := parseNums(bMain)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	// Equal numeric version: stable (no pre-release suffix) is newer than pre-release ("1.1.0" > "1.1.0-beta.1").
	if aPre == "" && bPre != "" {
		return 1
	}
	if aPre != "" && bPre == "" {
		return -1
	}
	return strings.Compare(aPre, bPre)
}

func splitPre(v string) (string, string) {
	if i := strings.IndexByte(v, '-'); i >= 0 {
		return v[:i], v[i+1:]
	}
	return v, ""
}

func parseNums(s string) [3]int {
	var out [3]int
	parts := strings.Split(s, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}
