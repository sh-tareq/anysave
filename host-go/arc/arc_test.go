package arc

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

var ctx = context.Background()

// contents opens an archive and reads every file.
func contents(t *testing.T, p, pw string) (map[string]string, Archive) {
	t.Helper()
	a, err := Open(p, Options{Password: pw})
	if err != nil {
		t.Fatalf("open %s: %v", filepath.Base(p), err)
	}
	out := map[string]string{}
	err = a.Walk(ctx, wantAll, func(e *Entry, r io.Reader) error {
		if r == nil {
			if e.Dir {
				out[e.Name+"/"] = ""
			}
			return nil
		}
		b, err := io.ReadAll(r)
		if err != nil {
			return fmt.Errorf("%s: %w", e.Name, err)
		}
		out[e.Name] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(p), err)
	}
	return out, a
}

func randText(n int) string {
	words := []string{"alpha ", "beta ", "গামা ", "delta ", "ε ", "zeta\n", "eta ", "theta "}
	var b strings.Builder
	var r [1]byte
	for b.Len() < n {
		rand.Read(r[:])
		b.WriteString(words[int(r[0])%len(words)])
	}
	return b.String()[:n]
}

func randBytes(n int) []byte { b := make([]byte, n); rand.Read(b); return b }

// sampleTree makes a folder of files to pack: text, random (unpackable) data, empty files,
// Unicode and long names, a nested empty folder.
func sampleTree(t *testing.T) (string, map[string]string) {
	dir := filepath.Join(t.TempDir(), "My Stuff")
	files := map[string]string{
		"My Stuff/readme.txt":                                   randText(50_000),
		"My Stuff/data/random.bin":                              string(randBytes(300_000)),
		"My Stuff/data/empty.txt":                               "",
		"My Stuff/বাংলা ফাইল.txt":                               "হ্যালো বিশ্ব",
		"My Stuff/日本語/ファイル.txt":                                 "こんにちは",
		"My Stuff/" + strings.Repeat("long name ", 20) + ".txt": "long",
		"My Stuff/photo.jpg":                                    string(randBytes(20_000)),
	}
	for name, body := range files {
		p := filepath.Join(filepath.Dir(dir), filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(dir, "empty folder"), 0o755)
	files["My Stuff/empty folder/"] = ""
	return dir, files
}

func sameFiles(t *testing.T, got, want map[string]string, what string) {
	t.Helper()
	for k, v := range want {
		if strings.HasSuffix(k, "/") {
			if _, ok := got[k]; !ok {
				t.Errorf("%s: folder %q missing", what, k)
			}
			continue
		}
		g, ok := got[k]
		if !ok {
			t.Errorf("%s: %q missing (have %v)", what, k, keys(got))
		} else if g != v {
			t.Errorf("%s: %q differs (%d vs %d bytes)", what, k, len(g), len(v))
		}
	}
}

func keys(m map[string]string) []string {
	var k []string
	for s := range m {
		k = append(k, s)
	}
	sort.Strings(k)
	return k
}

func bsdtar(t *testing.T, args ...string) string {
	t.Helper()
	bt := bsdtarPath()
	if bt == "" {
		t.Skip("no bsdtar")
	}
	cmd := exec.Command(bt, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bsdtar %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// ---------- making archives ----------

func TestCreateZipAndReadBack(t *testing.T) {
	src, want := sampleTree(t)
	for _, tc := range []struct {
		name string
		o    CreateOptions
	}{
		{"plain", CreateOptions{Format: "zip", Level: 5, Comment: "Hello from AnySave · বাংলা"}},
		{"store", CreateOptions{Format: "zip", Level: 0}},
		{"max", CreateOptions{Format: "zip", Level: 9}},
		{"aes", CreateOptions{Format: "zip", Level: 5, Password: "pässwörd 123"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.zip")
			res, err := Create(ctx, out, []string{src}, tc.o, nil)
			if err != nil {
				t.Fatal(err)
			}
			if res.Files != 7 || len(res.Parts) != 1 {
				t.Fatalf("result %+v", res)
			}
			got, a := contents(t, out, tc.o.Password)
			defer a.Close()
			sameFiles(t, got, want, tc.name)
			if a.Info().Comment != tc.o.Comment {
				t.Errorf("comment %q", a.Info().Comment)
			}
			if tc.o.Password != "" {
				if !a.Info().Encrypted {
					t.Error("not marked encrypted")
				}
				// Another program must be able to read it: libarchive (bsdtar) knows WinZip AES.
				dir := t.TempDir()
				bsdtar(t, "-xf", out, "-C", dir, "--passphrase", tc.o.Password)
				b, err := os.ReadFile(filepath.Join(dir, "My Stuff", "বাংলা ফাইল.txt"))
				if err != nil || string(b) != want["My Stuff/বাংলা ফাইল.txt"] {
					t.Errorf("bsdtar couldn't read our AES zip: %v %q", err, b)
				}
				b, _ = os.ReadFile(filepath.Join(dir, "My Stuff", "data", "random.bin"))
				if string(b) != want["My Stuff/data/random.bin"] {
					t.Error("bsdtar: random.bin differs")
				}
				// A wrong password is told apart from damage.
				a2, _ := Open(out, Options{Password: "nope"})
				err = a2.Walk(ctx, wantAll, func(e *Entry, r io.Reader) error {
					if r != nil {
						_, err := io.ReadAll(r)
						return err
					}
					return nil
				})
				if !errors.Is(err, ErrWrongPassword) {
					t.Errorf("wrong password gave %v", err)
				}
				a3, _ := Open(out, Options{})
				err = a3.Walk(ctx, wantAll, func(e *Entry, r io.Reader) error { return nil })
				if !errors.Is(err, ErrNeedPassword) {
					t.Errorf("no password gave %v", err)
				}
			} else if _, err := exec.LookPath("unzip"); err == nil {
				if out, err := exec.Command("unzip", "-tq", out).CombinedOutput(); err != nil {
					t.Errorf("unzip -t: %v %s", err, out)
				}
			}
		})
	}
}

func TestCreate7zAndReadBack(t *testing.T) {
	src, want := sampleTree(t)
	for _, tc := range []struct {
		name string
		o    CreateOptions
	}{
		{"normal", CreateOptions{Format: "7z", Level: 5}},
		{"solid", CreateOptions{Format: "7z", Level: 9, Solid: true}},
		{"store", CreateOptions{Format: "7z", Level: 0}},
		{"aes", CreateOptions{Format: "7z", Level: 3, Password: "secret ✓"}},
		{"aes-names", CreateOptions{Format: "7z", Level: 5, Password: "secret ✓", EncryptNames: true, Solid: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out.7z")
			if _, err := Create(ctx, out, []string{src}, tc.o, nil); err != nil {
				t.Fatal(err)
			}
			got, a := contents(t, out, tc.o.Password)
			info := a.Info()
			a.Close()
			sameFiles(t, got, want, tc.name)
			if tc.o.Solid != info.Solid {
				t.Errorf("solid = %v", info.Solid)
			}
			if tc.o.Password == "" {
				// libarchive reads plain 7z: a second, independent reader.
				dir := t.TempDir()
				bsdtar(t, "-xf", out, "-C", dir)
				b, err := os.ReadFile(filepath.Join(dir, "My Stuff", "readme.txt"))
				if err != nil || string(b) != want["My Stuff/readme.txt"] {
					t.Errorf("bsdtar couldn't read our 7z: %v", err)
				}
				return
			}
			if !info.Encrypted {
				t.Error("not marked encrypted")
			}
			_, err := Open(out, Options{})
			if tc.o.EncryptNames {
				if !errors.Is(err, ErrNeedPassword) {
					t.Errorf("names visible without a password: %v", err)
				}
				if _, err := Open(out, Options{Password: "wrong"}); !errors.Is(err, ErrWrongPassword) {
					t.Errorf("wrong password gave %v", err)
				}
				if !info.NamesEncrypted {
					t.Error("NamesEncrypted not set")
				}
			} else {
				if err != nil {
					t.Fatalf("names should be visible: %v", err)
				}
				a, _ := Open(out, Options{Password: "wrong"})
				_, err := Test(ctx, a, nil)
				if !errors.Is(err, ErrWrongPassword) {
					t.Errorf("wrong password test gave %v", err)
				}
			}
		})
	}
}

func TestSplitAndJoin(t *testing.T) {
	src, want := sampleTree(t)
	for _, format := range []string{"zip", "7z"} {
		t.Run(format, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "big."+format)
			res, err := Create(ctx, out, []string{src}, CreateOptions{Format: format, Level: 1, Split: 100_000, Password: "pw"}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Parts) < 3 || !strings.HasSuffix(res.Parts[0], "."+format+".001") {
				t.Fatalf("parts %v", res.Parts)
			}
			// Any part opens the whole set.
			got, a := contents(t, res.Parts[1], "pw")
			a.Close()
			sameFiles(t, got, want, "split "+format)
			joined, err := JoinParts(ctx, res.Parts[2], dir, nil)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Base(joined) != "big."+format {
				t.Errorf("joined name %s", joined)
			}
			got, a = contents(t, joined, "pw")
			a.Close()
			sameFiles(t, got, want, "joined "+format)
		})
	}
	// A set that fits in one part is just a normal file.
	out := filepath.Join(t.TempDir(), "small.zip")
	res, err := Create(ctx, out, []string{src}, CreateOptions{Format: "zip", Split: 100 << 20}, nil)
	if err != nil || len(res.Parts) != 1 || res.Parts[0] != out {
		t.Errorf("one part: %v %v", res, err)
	}
}

// The old Info-ZIP split style (.z01, .z02 … .zip), made by the Mac's own zip tool.
func TestInfoZipSplit(t *testing.T) {
	zipBin, err := exec.LookPath("zip")
	if err != nil {
		t.Skip("no zip tool")
	}
	src, want := sampleTree(t)
	dir := t.TempDir()
	cmd := exec.Command(zipBin, "-r", "-q", "-s", "64k", filepath.Join(dir, "set.zip"), "My Stuff")
	cmd.Dir = filepath.Dir(src)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("zip -s: %v %s", err, out)
	}
	parts, kind := Parts(filepath.Join(dir, "set.z02"))
	if kind != "zsplit" || len(parts) < 3 {
		t.Fatalf("parts %v %s", parts, kind)
	}
	got, a := contents(t, filepath.Join(dir, "set.zip"), "")
	a.Close()
	// zip stores names as the Mac gives them (decomposed Unicode): compare only ASCII names here.
	for k, v := range want {
		if strings.Trim(k, "abcdefghijklmnopqrstuvwxyzMS/. ") == "" || k == "My Stuff/data/random.bin" {
			if !strings.HasSuffix(k, "/") && got[k] != v {
				t.Errorf("%s differs or missing", k)
			}
		}
	}
	joined, err := JoinParts(ctx, parts[0], t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("unzip", "-tq", joined).CombinedOutput(); err != nil {
		t.Errorf("joined zip doesn't test: %v %s", err, out)
	}
}

// ---------- reading archives made by other programs ----------

func TestRealRAR(t *testing.T) {
	for _, tc := range []struct {
		file, pw string
		files    map[string]uint32 // name → CRC
		version  string
	}{
		{"rar5-subdirs.rar", "", map[string]uint32{"sub/dir1/file1.txt": 0xe229f704, "sub/with space/long fn.txt": 0x71b7247d, "sub/üȵĩöḋè/file.txt": 0x2fec89c1}, "RAR5"},
		{"rar3-subdirs.rar", "", nil, "RAR4"},
		{"rar5-solid.rar", "", nil, "RAR5"},
		{"rar3-solid.rar", "", nil, "RAR4"},
		{"rar5-psw.rar", "password", nil, "RAR5"},
		{"rar5-hpsw.rar", "password", nil, "RAR5"},
		{"rar3-comment-psw.rar", "password", nil, "RAR4"},
		{"rar3-comment-hpsw.rar", "password", nil, "RAR4"},
		{"rar5-vols.part2.rar", "", map[string]uint32{"vols/bigfile.txt": 0x509ad74c, "vols/smallfile.txt": 0xd08a1f86}, "RAR5"},
		{"unicode.rar", "", map[string]uint32{"уииоотивл.txt": 0x6751fc53, "𝐀𝐁𝐁𝐂.txt": 0x6751fc53}, "RAR4"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			p := filepath.Join("testdata", tc.file)
			got, a := contents(t, p, tc.pw)
			info := a.Info()
			if info.Format != "rar" || info.Version != tc.version {
				t.Errorf("info %+v", info)
			}
			if len(got) == 0 {
				t.Fatal("nothing read")
			}
			for name, crc := range tc.files {
				if crc32.ChecksumIEEE([]byte(got[name])) != crc {
					t.Errorf("%s: wrong content (have %v)", name, keys(got))
				}
			}
			if tc.file == "rar5-psw.rar" && (len(got["stest1.txt"]) != 2048 || got["stest1.txt"] != got["stest2.txt"]) {
				t.Error("rar5-psw content") // its stored CRCs are password-tweaked, so compare the two copies
			}
			if tc.pw != "" && tc.file != "rar3-comment-psw.rar" { // that one's files are empty: nothing to check a password against
				if !info.Encrypted {
					t.Error("not marked encrypted")
				}
				a, err := Open(p, Options{})
				if err == nil {
					_, err = Test(ctx, a, nil)
				}
				if !errors.Is(err, ErrNeedPassword) {
					t.Errorf("no password: %v", err)
				}
				a, err = Open(p, Options{Password: "wrong"})
				if err == nil {
					var res *TestResult
					res, err = Test(ctx, a, nil)
					if err == nil && len(res.Bad) > 0 {
						err = ErrWrongPassword // RAR4 has no password check: it shows up as bad checksums
					}
				}
				if !errors.Is(err, ErrWrongPassword) {
					t.Errorf("wrong password: %v", err)
				}
			}
			if strings.Contains(tc.file, "vols") && len(info.Volumes) != 3 {
				t.Errorf("volumes %v", info.Volumes)
			}
		})
	}
}

func TestReal7z(t *testing.T) {
	for _, tc := range []struct{ file, pw string }{
		{"t2.7z", "password"}, {"t3.7z", "password"}, {"t4.7z", "password"}, {"lzma2.7z", ""}, {"multi.7z.003", ""},
	} {
		got, a := contents(t, filepath.Join("testdata", tc.file), tc.pw)
		if len(got) == 0 {
			t.Errorf("%s: nothing read", tc.file)
		}
		res, err := Test(ctx, a, nil)
		if err != nil || len(res.Bad) > 0 {
			t.Errorf("%s: test %v %+v", tc.file, err, res)
		}
		a.Close()
	}
	if _, err := Open(filepath.Join("testdata", "t3.7z"), Options{}); !errors.Is(err, ErrNeedPassword) {
		t.Errorf("t3 without password: %v", err)
	}
}

func TestTarAndCompressed(t *testing.T) {
	src, want := sampleTree(t)
	dir := t.TempDir()
	for _, f := range []struct{ name, flag string }{{"a.tar", ""}, {"a.tar.gz", "z"}, {"a.tgz", "z"}, {"a.tar.bz2", "j"}, {"a.tar.xz", "J"}} {
		out := filepath.Join(dir, f.name)
		bsdtar(t, "-c"+f.flag+"f", out, "-C", filepath.Dir(src), "My Stuff")
		got, a := contents(t, out, "")
		a.Close()
		sameFiles(t, got, want, f.name)
	}
	// .tar.zst, made with the Zstandard library
	tarFile := filepath.Join(dir, "a.tar")
	raw, _ := os.ReadFile(tarFile)
	var zb bytes.Buffer
	zw, _ := zstd.NewWriter(&zb)
	zw.Write(raw)
	zw.Close()
	os.WriteFile(filepath.Join(dir, "a.tar.zst"), zb.Bytes(), 0o644)
	got, a := contents(t, filepath.Join(dir, "a.tar.zst"), "")
	a.Close()
	sameFiles(t, got, want, "tar.zst")
	// A single gzipped file.
	gz := filepath.Join(dir, "notes.txt.gz")
	bsdtar(t, "-czf", filepath.Join(dir, "unused.tgz"), "-C", dir, "a.tar")
	cmd := exec.Command("gzip", "-k", "-c", filepath.Join(filepath.Dir(src), "My Stuff", "readme.txt"))
	if b, err := cmd.Output(); err == nil {
		os.WriteFile(gz, b, 0o644)
		got, a := contents(t, gz, "")
		if a.Info().Format != "gz" || len(got) != 1 {
			t.Errorf("single gz: %+v %v", a.Info(), keys(got))
		}
		for _, v := range got {
			if v != want["My Stuff/readme.txt"] {
				t.Error("single gz content differs")
			}
		}
		a.Close()
	}
}

func TestISO(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("hdiutil is a Mac tool")
	}
	src, want := sampleTree(t)
	out := filepath.Join(t.TempDir(), "disk.iso")
	if b, err := exec.Command("hdiutil", "makehybrid", "-iso", "-joliet", "-o", out, src).CombinedOutput(); err != nil {
		t.Skipf("hdiutil: %v %s", err, b)
	}
	got, a := contents(t, out, "")
	defer a.Close()
	if a.Info().Format != "iso" {
		t.Errorf("format %s", a.Info().Format)
	}
	if got["readme.txt"] != want["My Stuff/readme.txt"] || got["data/random.bin"] != want["My Stuff/data/random.bin"] {
		t.Errorf("iso content: %v", keys(got))
	}
}

// ---------- safety ----------

func craftedZip(t *testing.T, names ...string) string {
	p := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(p)
	w := zip.NewWriter(f)
	for _, n := range names {
		fw, err := w.CreateHeader(&zip.FileHeader{Name: n, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		fw.Write([]byte("payload of " + n))
	}
	w.Close()
	f.Close()
	return p
}

func TestZipSlipBlocked(t *testing.T) {
	p := craftedZip(t, "../evil.txt", "ok/../../evil2.txt", "/abs.txt", `..\win-evil.txt`, "C:/drive.txt", "good.txt")
	a, err := Open(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	s := Check(a)
	if len(s.Unsafe) != 3 {
		t.Errorf("unsafe %+v", s.Unsafe)
	}
	root := t.TempDir()
	dest := filepath.Join(root, "inside", "deeper")
	res, err := Extract(ctx, a, ExtractOptions{Dest: dest, Name: "evil", Mode: "folder"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Skipped) != 3 {
		t.Errorf("skipped %+v", res.Skipped)
	}
	// Nothing may appear outside dest.
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if !info.IsDir() && !strings.HasPrefix(p, dest) {
			t.Errorf("escaped: %s", p)
		}
		return nil
	})
	for _, n := range []string{"good.txt", "abs.txt", "drive.txt"} {
		if _, err := os.Stat(filepath.Join(dest, "evil", n)); err != nil {
			t.Errorf("%s not unpacked: %v", n, err)
		}
	}
}

func TestRarSymlinkTraversalBlocked(t *testing.T) {
	a, err := Open(filepath.Join("testdata", "rar5-evil-symlink-traversal.rar"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	root := t.TempDir()
	dest := filepath.Join(root, "out")
	res, err := Extract(ctx, a, ExtractOptions{Dest: dest, Name: "x", Mode: "folder"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "pwned.txt")); err == nil {
		t.Fatal("the symlink trick wrote outside the folder")
	}
	filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if info.Mode()&os.ModeSymlink != 0 {
			t.Errorf("a link was made: %s", p)
		}
		return nil
	})
	if len(res.Skipped) == 0 {
		t.Error("the link wasn't reported")
	}
}

func TestZipBombOverlapRefused(t *testing.T) {
	p := filepath.Join(t.TempDir(), "bomb.zip")
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for i := 0; i < 3; i++ {
		fw, _ := w.Create(fmt.Sprintf("f%d.txt", i))
		fw.Write(bytes.Repeat([]byte{'A'}, 100000))
	}
	w.Close()
	b := buf.Bytes()
	// Point every central directory entry at the first file's data.
	first := -1
	for i := 0; i+4 <= len(b); i++ {
		if bytes.Equal(b[i:i+4], sigCen) {
			if first < 0 {
				first = int(binary.LittleEndian.Uint32(b[i+42:]))
			} else {
				binary.LittleEndian.PutUint32(b[i+42:], uint32(first))
			}
		}
	}
	os.WriteFile(p, b, 0o644)
	a, err := Open(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if Check(a).Block == "" {
		t.Error("overlap not noticed")
	}
	_, err = Extract(ctx, a, ExtractOptions{Dest: t.TempDir()})
	var be *BombError
	if !errors.As(err, &be) {
		t.Errorf("extract gave %v", err)
	}
}

func TestSizeLieStopped(t *testing.T) {
	// A ZIP whose header says 10 bytes but whose data is 1 MB.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.Create("liar.txt")
	fw.Write(bytes.Repeat([]byte{'x'}, 1<<20))
	w.Close()
	b := buf.Bytes()
	for i := 0; i+4 <= len(b); i++ {
		if bytes.Equal(b[i:i+4], sigCen) {
			binary.LittleEndian.PutUint32(b[i+24:], 10)
		}
	}
	p := filepath.Join(t.TempDir(), "liar.zip")
	os.WriteFile(p, b, 0o644)
	a, err := Open(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	dest := t.TempDir()
	if _, err := Extract(ctx, a, ExtractOptions{Dest: dest}); !isDamage(err) {
		t.Errorf("got %v", err)
	}
	if ents, _ := os.ReadDir(dest); len(ents) != 0 {
		t.Errorf("left behind: %v", ents)
	}
}

func TestNoDiskSpace(t *testing.T) {
	src, _ := sampleTree(t)
	out := filepath.Join(t.TempDir(), "a.zip")
	Create(ctx, out, []string{src}, CreateOptions{Format: "zip"}, nil)
	a, _ := Open(out, Options{})
	defer a.Close()
	_, err := Extract(ctx, a, ExtractOptions{Dest: t.TempDir(), FreeSpace: func(string) int64 { return 1000 }})
	var be *BombError
	if !errors.As(err, &be) {
		t.Errorf("got %v", err)
	}
}

// ---------- damaged archives ----------

func TestDamagedZip(t *testing.T) {
	src, want := sampleTree(t)
	dir := t.TempDir()
	good := filepath.Join(dir, "good.zip")
	Create(ctx, good, []string{src}, CreateOptions{Format: "zip", Level: 5}, nil)
	b, _ := os.ReadFile(good)

	// 1. The list at the end is gone (an unfinished download of the last bytes).
	cut := filepath.Join(dir, "cut.zip")
	os.WriteFile(cut, b[:bytes.Index(b, sigCen)+10], 0o644)
	a, err := Open(cut, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !a.Info().Recovered || len(a.Entries()) != 11 {
		t.Errorf("recovered=%v entries=%d", a.Info().Recovered, len(a.Entries()))
	}
	got, _ := contents(t, cut, "")
	sameFiles(t, got, want, "no central directory")
	a.Close()
	// Cut in the middle of a file: the files before it are saved, the cut one is reported.
	cut2 := filepath.Join(dir, "cut2.zip")
	os.WriteFile(cut2, b[:200_000], 0o644)
	a, err = Open(cut2, Options{})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Extract(ctx, a, ExtractOptions{Dest: filepath.Join(dir, "x"), KeepGoing: true})
	if err != nil || res.Files != 1 { // empty.txt (random.bin is cut)
		t.Errorf("salvage: %v %+v", err, res)
	}
	a.Close()

	// 2. A flipped byte inside readme.txt's data.
	bad := append([]byte(nil), b...)
	i := bytes.Index(bad, []byte("readme.txt"))
	bad[i+200] ^= 0xff
	broken := filepath.Join(dir, "broken.zip")
	os.WriteFile(broken, bad, 0o644)
	a, _ = Open(broken, Options{})
	tr, err := Test(ctx, a, nil)
	a.Close()
	if err != nil || len(tr.Bad) != 1 || tr.Bad[0].Name != "My Stuff/readme.txt" {
		t.Errorf("test: %v %+v", err, tr)
	}
	fixed := filepath.Join(dir, "fixed.zip")
	rr, err := RepairZip(ctx, []string{broken}, fixed, "", nil)
	if err != nil || len(rr.Dropped) != 1 {
		t.Fatalf("repair: %v %+v", err, rr)
	}
	got, a = contents(t, fixed, "")
	a.Close()
	delete(want, "My Stuff/readme.txt")
	sameFiles(t, got, want, "repaired")
	if out, err := exec.Command("unzip", "-tq", fixed).CombinedOutput(); err != nil {
		t.Errorf("unzip -t repaired: %v %s", err, out)
	}

	// 3. Not an archive at all.
	junk := filepath.Join(dir, "junk.zip")
	os.WriteFile(junk, randBytes(5000), 0o644)
	if _, err := Open(junk, Options{}); err == nil {
		t.Error("junk opened")
	}
}

func TestDamaged7zAndRar(t *testing.T) {
	src, _ := sampleTree(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "a.7z")
	Create(ctx, p, []string{src}, CreateOptions{Format: "7z", Level: 5}, nil)
	b, _ := os.ReadFile(p)
	b[100] ^= 0xff
	os.WriteFile(p, b, 0o644)
	a, err := Open(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Test(ctx, a, nil)
	a.Close()
	if err != nil || len(tr.Bad) == 0 {
		t.Errorf("7z damage not found: %v %+v", err, tr)
	}
	rb, _ := os.ReadFile(filepath.Join("testdata", "rar5-subdirs.rar"))
	rb[len(rb)/2] ^= 0xff
	rp := filepath.Join(dir, "a.rar")
	os.WriteFile(rp, rb, 0o644)
	a, err = Open(rp, Options{})
	if err == nil {
		tr, err = Test(ctx, a, nil)
		if err == nil && len(tr.Bad) == 0 {
			t.Error("rar damage not found")
		}
	}
}

// ---------- unpacking: smart folders, duplicate names, selection ----------

func TestSmartUnpackAndPolicies(t *testing.T) {
	src, _ := sampleTree(t)
	work := t.TempDir()
	one := filepath.Join(work, "one.zip") // one top folder inside
	Create(ctx, one, []string{src}, CreateOptions{Format: "zip"}, nil)
	many := filepath.Join(work, "many.zip") // loose files inside
	Create(ctx, many, []string{filepath.Join(src, "readme.txt"), filepath.Join(src, "photo.jpg")}, CreateOptions{Format: "zip"}, nil)

	dest := filepath.Join(work, "out")
	a, _ := Open(one, Options{})
	res, err := Extract(ctx, a, ExtractOptions{Dest: dest, Name: "one"})
	if err != nil || res.Path != filepath.Join(dest, "My Stuff") {
		t.Fatalf("smart one-folder: %v %+v", err, res)
	}
	// Again: the name is taken → kept both.
	res, _ = Extract(ctx, a, ExtractOptions{Dest: dest, Name: "one"})
	if res.Path != filepath.Join(dest, "My Stuff (2)") || res.Existing != 1 {
		t.Errorf("rename policy: %+v", res)
	}
	// Skip: nothing new.
	os.WriteFile(filepath.Join(dest, "My Stuff", "readme.txt"), []byte("mine"), 0o644)
	Extract(ctx, a, ExtractOptions{Dest: dest, Name: "one", Policy: "skip"})
	if b, _ := os.ReadFile(filepath.Join(dest, "My Stuff", "readme.txt")); string(b) != "mine" {
		t.Error("skip overwrote")
	}
	// Replace: overwritten.
	Extract(ctx, a, ExtractOptions{Dest: dest, Name: "one", Policy: "replace"})
	if b, _ := os.ReadFile(filepath.Join(dest, "My Stuff", "readme.txt")); string(b) == "mine" {
		t.Error("replace didn't overwrite")
	}
	a.Close()

	a, _ = Open(many, Options{})
	res, _ = Extract(ctx, a, ExtractOptions{Dest: dest, Name: "many"})
	if res.Path != filepath.Join(dest, "many") {
		t.Errorf("smart many: %+v", res)
	}
	// "Here": straight into the folder.
	here := filepath.Join(work, "here")
	res, _ = Extract(ctx, a, ExtractOptions{Dest: here, Mode: "here"})
	if len(res.Items) != 2 || res.Path != here {
		t.Errorf("here: %+v", res)
	}
	a.Close()

	// Selected items from inside a folder, without the folder path.
	a, _ = Open(one, Options{})
	sel := filepath.Join(work, "sel")
	res, err = Extract(ctx, a, ExtractOptions{Dest: sel, Mode: "here", Only: []string{"My Stuff/data"}, Base: "My Stuff"})
	if err != nil || res.Files != 2 {
		t.Fatalf("selected: %v %+v", err, res)
	}
	if _, err := os.Stat(filepath.Join(sel, "data", "random.bin")); err != nil {
		t.Error("selected folder not where expected")
	}
	a.Close()
	// No hidden staging folders left behind anywhere.
	filepath.Walk(work, func(p string, info os.FileInfo, err error) error {
		if strings.Contains(p, ".anysave-unpacking-") {
			t.Errorf("left behind: %s", p)
		}
		return nil
	})
}

func TestCancelLeavesNothing(t *testing.T) {
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "big.bin"), bytes.Repeat(randBytes(1<<20), 40), 0o644)
	out := filepath.Join(t.TempDir(), "big.zip")
	Create(ctx, out, []string{src}, CreateOptions{Format: "zip", Level: 1}, nil)
	a, _ := Open(out, Options{})
	defer a.Close()
	c, cancel := context.WithCancel(ctx)
	dest := t.TempDir()
	_, err := Extract(c, a, ExtractOptions{Dest: dest, Progress: func(done, total int64, _ string) {
		if done > 5<<20 {
			cancel()
		}
	}})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("got %v", err)
	}
	if ents, _ := os.ReadDir(dest); len(ents) != 0 {
		t.Errorf("left: %v", ents)
	}
	// Cancelled while making: no half archive.
	c2, cancel2 := context.WithCancel(ctx)
	out2 := filepath.Join(t.TempDir(), "x.7z")
	_, err = Create(c2, out2, []string{src}, CreateOptions{Format: "7z", Level: 1}, func(done, total int64, _ string) {
		if done > 2<<20 {
			cancel2()
		}
	})
	if !errors.Is(err, ErrCancelled) {
		t.Errorf("create cancel: %v", err)
	}
	if _, err := os.Stat(out2); err == nil {
		t.Error("half archive left")
	}
}

// ---------- changing archives ----------

func TestEditZipAnd7z(t *testing.T) {
	src, want := sampleTree(t)
	extra := filepath.Join(t.TempDir(), "added.txt")
	os.WriteFile(extra, []byte("new file"), 0o644)
	for _, tc := range []struct{ format, pw string }{{"zip", ""}, {"zip", "pw"}, {"7z", ""}, {"7z", "pw"}} {
		t.Run(tc.format+tc.pw, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "e."+tc.format)
			if _, err := Create(ctx, p, []string{src}, CreateOptions{Format: tc.format, Password: tc.pw, Comment: "old"}, nil); err != nil {
				t.Fatal(err)
			}
			if tc.pw != "" {
				err := Edit(ctx, p, EditOptions{Add: []string{extra}}, nil)
				if !errors.Is(err, ErrNeedPassword) {
					t.Errorf("adding to a locked archive without the password: %v", err)
				}
			}
			o := EditOptions{Password: tc.pw, Delete: []string{"My Stuff/photo.jpg"},
				Rename: map[string]string{"My Stuff/data": "My Stuff/Data Files", "My Stuff/readme.txt": "My Stuff/READ ME.txt"},
				Add:    []string{extra}, AddTo: "My Stuff/data"}
			if tc.format == "zip" {
				c := "new comment ✓"
				o.Comment = &c
			}
			if err := Edit(ctx, p, o, nil); err != nil {
				t.Fatal(err)
			}
			got, a := contents(t, p, tc.pw)
			a.Close()
			w := map[string]string{}
			for k, v := range want {
				switch {
				case k == "My Stuff/photo.jpg":
				case k == "My Stuff/readme.txt":
					w["My Stuff/READ ME.txt"] = v
				case strings.HasPrefix(k, "My Stuff/data/"):
					w["My Stuff/Data Files/"+strings.TrimPrefix(k, "My Stuff/data/")] = v
				default:
					w[k] = v
				}
			}
			w["My Stuff/data/added.txt"] = "new file"
			sameFiles(t, got, w, "edited")
			if _, ok := got["My Stuff/photo.jpg"]; ok {
				t.Error("not deleted")
			}
			if tc.format == "zip" && a.Info().Comment != "new comment ✓" {
				t.Errorf("comment %q", a.Info().Comment)
			}
			if tc.format == "zip" && tc.pw != "" {
				// The added file is locked too.
				for _, e := range a.Entries() {
					if e.Name == "My Stuff/data/added.txt" && !e.Encrypted {
						t.Error("added file isn't locked")
					}
				}
				dir := t.TempDir()
				bsdtar(t, "-xf", p, "-C", dir, "--passphrase", tc.pw)
			}
			if tc.format == "zip" && tc.pw == "" {
				if out, err := exec.Command("unzip", "-tq", p).CombinedOutput(); err != nil {
					t.Errorf("unzip -t: %v %s", err, out)
				}
			}
			// A name clash is refused, and the archive is left as it was.
			before, _ := os.ReadFile(p)
			err := Edit(ctx, p, EditOptions{Password: tc.pw, Rename: map[string]string{"My Stuff/READ ME.txt": "My Stuff/বাংলা ফাইল.txt"}}, nil)
			if err == nil {
				t.Error("clash allowed")
			}
			after, _ := os.ReadFile(p)
			if !bytes.Equal(before, after) {
				t.Error("archive changed after a refused edit")
			}
		})
	}
}

func TestConvert(t *testing.T) {
	// RAR → ZIP and RAR → locked 7z, straight through.
	a, err := Open(filepath.Join("testdata", "rar5-subdirs.rar"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := contents(t, filepath.Join("testdata", "rar5-subdirs.rar"), "")
	for _, o := range []CreateOptions{{Format: "zip"}, {Format: "7z", Password: "x", EncryptNames: true}} {
		out := filepath.Join(t.TempDir(), "c."+o.Format)
		if _, err := Convert(ctx, a, out, o, nil); err != nil {
			t.Fatal(err)
		}
		got, b := contents(t, out, o.Password)
		b.Close()
		sameFiles(t, got, want, "convert to "+o.Format)
	}
}

func TestCP437Names(t *testing.T) {
	// Old Windows ZIP: "Résumé.txt" in the DOS code page, no UTF-8 flag.
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	fw, _ := w.CreateHeader(&zip.FileHeader{Name: "R\x82sum\x82.txt", NonUTF8: true})
	fw.Write([]byte("cv"))
	w.Close()
	p := filepath.Join(t.TempDir(), "old.zip")
	os.WriteFile(p, buf.Bytes(), 0o644)
	got, a := contents(t, p, "")
	a.Close()
	if got["Résumé.txt"] != "cv" {
		t.Errorf("names: %v", keys(got))
	}
}

func TestLongNameShortened(t *testing.T) {
	long := strings.Repeat("ঝ", 120) + ".txt" // 364 bytes: too long for most disks
	p := craftedZip(t, long)
	a, _ := Open(p, Options{})
	defer a.Close()
	dest := t.TempDir()
	res, err := Extract(ctx, a, ExtractOptions{Dest: dest, Mode: "here"})
	if err != nil || res.Files != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	if !strings.HasSuffix(res.Path, ".txt") || len(filepath.Base(res.Path)) > 255 {
		t.Errorf("name %q", filepath.Base(res.Path))
	}
}

func TestSevenZipSpeed(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	src := t.TempDir()
	os.WriteFile(filepath.Join(src, "text.txt"), []byte(randText(20<<20)), 0o644)
	for _, lvl := range []int{1, 5, 9} {
		out := filepath.Join(t.TempDir(), "s.7z")
		start := time.Now()
		Create(ctx, out, []string{src}, CreateOptions{Format: "7z", Level: lvl}, nil)
		st, _ := os.Stat(out)
		t.Logf("7z level %d: 20 MB of text → %s in %v", lvl, Bytes(st.Size()), time.Since(start).Round(time.Millisecond))
	}
	out := filepath.Join(t.TempDir(), "s.zip")
	start := time.Now()
	Create(ctx, out, []string{src}, CreateOptions{Format: "zip", Level: 5}, nil)
	st, _ := os.Stat(out)
	t.Logf("zip level 5: 20 MB of text → %s in %v", Bytes(st.Size()), time.Since(start).Round(time.Millisecond))
}
