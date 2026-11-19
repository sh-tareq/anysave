// Package arc is AnySave's archive engine: it opens ZIP, 7z, RAR (4 and 5), TAR (plain, .gz, .bz2,
// .xz, .zst), single compressed files and ISO images, unpacks them safely, and makes ZIP and 7z
// archives (with AES-256 passwords, levels, solid blocks and split parts).
//
// Everything is pure Go except ISO images, which are read through the system's bsdtar (built into
// macOS and Windows 10+). RAR is read with github.com/nwaples/rardecode (BSD-2); RAR archives are
// never made, because RARLAB's licence doesn't allow it.
package arc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Errors that the app turns into plain words.
var (
	ErrNeedPassword  = errors.New("this archive is locked with a password")
	ErrWrongPassword = errors.New("wrong password")
	ErrCancelled     = errors.New("cancelled")
)

// DamagedError says the archive (or one file in it) can't be read as it is.
type DamagedError struct{ Detail string }

func (e *DamagedError) Error() string { return "damaged archive: " + e.Detail }

// UnsupportedError says the file isn't an archive AnySave can open, or uses something it can't read.
type UnsupportedError struct{ Detail string }

func (e *UnsupportedError) Error() string { return e.Detail }

func damaged(format string, a ...any) error {
	return &DamagedError{Detail: fmt.Sprintf(format, a...)}
}

// Entry is one file or folder inside an archive.
type Entry struct {
	Index     int         `json:"i"`
	Name      string      `json:"name"` // cleaned, "/"-separated, never starts with "/"
	Size      int64       `json:"size"`
	Packed    int64       `json:"packed"` // -1 when unknown
	Modified  time.Time   `json:"mtime"`
	Dir       bool        `json:"dir,omitempty"`
	Link      string      `json:"link,omitempty"` // symlink / hard link target
	Encrypted bool        `json:"enc,omitempty"`
	Method    string      `json:"method,omitempty"`
	Unsafe    string      `json:"unsafe,omitempty"` // why it would escape the folder (never unpacked)
	Special   bool        `json:"special,omitempty"`
	Mode      fs.FileMode `json:"-"`
	CRC       uint32      `json:"-"`
	HasCRC    bool        `json:"-"`
	raw       string
	ref       any // reader-specific handle
}

// Info describes the archive itself.
type Info struct {
	Format         string   `json:"format"` // zip, 7z, rar, tar, tar.gz, gz, iso, …
	Comment        string   `json:"comment,omitempty"`
	Solid          bool     `json:"solid,omitempty"`
	Encrypted      bool     `json:"encrypted,omitempty"`      // some contents need a password
	NamesEncrypted bool     `json:"namesEncrypted,omitempty"` // the list itself needed the password
	Volumes        []string `json:"volumes,omitempty"`        // the parts, when split
	Size           int64    `json:"size"`                     // bytes on disk (all parts)
	Editable       bool     `json:"editable,omitempty"`       // AnySave can add/delete/rename inside
	Version        string   `json:"version,omitempty"`        // e.g. "RAR5"
	Recovered      bool     `json:"recovered,omitempty"`      // the list was rebuilt from a damaged file
	Warning        string   `json:"warning,omitempty"`        // a reason not to unpack (zip bomb trick)
}

const (
	modeType    = fs.ModeType
	modeSymlink = fs.ModeSymlink
)

// unixMode turns Unix st_mode bits into an fs.FileMode.
func unixMode(m uint32) fs.FileMode {
	mode := fs.FileMode(m & 0o777)
	switch m & 0xf000 {
	case 0x4000:
		mode |= fs.ModeDir
	case 0xa000:
		mode |= fs.ModeSymlink
	case 0x2000, 0x6000:
		mode |= fs.ModeDevice
	case 0x1000:
		mode |= fs.ModeNamedPipe
	case 0xc000:
		mode |= fs.ModeSocket
	}
	return mode
}

// Archive is an open archive.
type Archive interface {
	Info() Info
	Entries() []*Entry
	// Walk calls fn, in the archive's own order, for every entry want accepts, with a reader of its
	// data (nil for folders and links). Stopping early: return errStop from fn.
	Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error
	Close() error
}

// ErrStop ends a Walk early without an error.
var ErrStop = errors.New("stop")

// Options for Open.
type Options struct {
	Password string
}

// Open opens an archive by its content (not only its name). For split sets, give any part.
func Open(p string, o Options) (Archive, error) {
	parts, kind := Parts(p)
	var src *multiFile
	var err error
	if len(parts) > 1 || kind != "" {
		src, err = openMulti(parts)
	} else {
		src, err = openMulti([]string{p})
	}
	if err != nil {
		return nil, err
	}
	a, err := openSource(src, parts, kind, o)
	if err != nil {
		src.Close()
		return nil, err
	}
	return a, nil
}

func openSource(src *multiFile, parts []string, kind string, o Options) (Archive, error) {
	head := make([]byte, 64<<10)
	n, _ := src.ReadAt(head, 0)
	head = head[:n]
	first := parts[0]
	switch {
	case kind == "rar" || bytes.HasPrefix(head, rarSig4) || bytes.HasPrefix(head, rarSig5):
		src.Close() // rardecode opens the volumes itself
		return openRar(first, parts, o)
	case kind == "zsplit":
		return openZipScan(src, parts, o, false)
	case bytes.HasPrefix(head, sig7z):
		return open7z(src, parts, o)
	case bytes.HasPrefix(head, []byte("PK\x03\x04")) || bytes.HasPrefix(head, []byte("PK\x05\x06")) || bytes.HasPrefix(head, []byte("PK\x07\x08")):
		return openZip(src, parts, o)
	case isISO(src):
		return openISO(src, parts)
	}
	if c := compressionOf(head); c != "" {
		return openCompressed(src, parts, c, filepath.Base(first))
	}
	if isTarHeader(head) {
		return openTar(src, parts, "")
	}
	// Self-extracting or otherwise prefixed archives: look for a ZIP or 7z inside.
	if bytes.Contains(head, sig7z) {
		return open7z(src, parts, o)
	}
	if ok, _ := hasZipEnd(src); ok {
		return openZip(src, parts, o)
	}
	return nil, &UnsupportedError{Detail: "This isn't an archive AnySave can open."}
}

// ---------- names ----------

// cleanName turns a stored name into a safe relative path, or says why it's unsafe.
func cleanName(raw string) (string, string) {
	n := strings.ReplaceAll(raw, "\\", "/")
	unsafe := ""
	if strings.HasPrefix(n, "/") {
		unsafe = "absolute path"
	}
	if len(n) >= 2 && n[1] == ':' && ((n[0] >= 'a' && n[0] <= 'z') || (n[0] >= 'A' && n[0] <= 'Z')) {
		n = n[2:]
		unsafe = "absolute path"
	}
	var out []string
	for _, c := range strings.Split(n, "/") {
		switch c {
		case "", ".":
			continue
		case "..":
			return strings.Trim(n, "/"), "goes outside the folder (../)"
		}
		out = append(out, c)
	}
	return strings.Join(out, "/"), unsafe
}

func finishEntries(es []*Entry) {
	for i, e := range es {
		e.Index = i
		name, why := cleanName(e.raw)
		if why == "absolute path" {
			why = "" // the leading "/" or "C:" is dropped, as every unpacker does
		}
		e.Name = name
		e.Unsafe = why
		if name == "" && why == "" {
			e.Unsafe = "empty name"
		}
		if e.Link != "" && !e.Dir {
			e.Special = true
		}
	}
}

// ---------- reading helpers ----------

// multiFile is a ReaderAt over one file or a set of parts joined end to end.
type multiFile struct {
	files []*os.File
	offs  []int64 // start of each part
	size  int64
}

func openMulti(paths []string) (*multiFile, error) {
	m := &multiFile{}
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			m.Close()
			return nil, err
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			m.Close()
			return nil, err
		}
		m.files = append(m.files, f)
		m.offs = append(m.offs, m.size)
		m.size += st.Size()
	}
	return m, nil
}

func (m *multiFile) Size() int64 { return m.size }

func (m *multiFile) Close() error {
	for _, f := range m.files {
		f.Close()
	}
	m.files = nil
	return nil
}

func (m *multiFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= m.size {
		return 0, io.EOF
	}
	total := 0
	for len(p) > 0 && off < m.size {
		i := sort.Search(len(m.offs), func(i int) bool { return m.offs[i] > off }) - 1
		end := m.size
		if i+1 < len(m.offs) {
			end = m.offs[i+1]
		}
		chunk := p
		if int64(len(chunk)) > end-off {
			chunk = chunk[:end-off]
		}
		n, err := m.files[i].ReadAt(chunk, off-m.offs[i])
		total += n
		off += int64(n)
		p = p[n:]
		if err != nil && err != io.EOF {
			return total, err
		}
		if n < len(chunk) {
			return total, io.ErrUnexpectedEOF
		}
	}
	if len(p) > 0 {
		return total, io.EOF
	}
	return total, nil
}

func (m *multiFile) reader() io.Reader { return io.NewSectionReader(m, 0, m.size) }

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if c.ctx.Err() != nil {
		return 0, ErrCancelled
	}
	return c.r.Read(p)
}

func wantAll(*Entry) bool { return true }

func baseInfo(format string, src *multiFile, parts []string) Info {
	i := Info{Format: format, Size: src.size}
	if len(parts) > 1 {
		i.Volumes = parts
	}
	return i
}

func sortedCopy(es []*Entry) []*Entry {
	out := append([]*Entry(nil), es...)
	sort.SliceStable(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// Kind is a short name for the format of a file name (for the page), "" if it isn't an archive name.
func Kind(name string) string {
	l := strings.ToLower(name)
	for _, s := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".tbz", ".txz", ".tzst"} {
		if strings.HasSuffix(l, s) {
			return "tar"
		}
	}
	switch path.Ext(l) {
	case ".zip", ".cbz", ".jar", ".apk", ".epub", ".z01", ".zipx":
		return "zip"
	case ".7z", ".cb7":
		return "7z"
	case ".rar", ".cbr", ".r00":
		return "rar"
	case ".tar":
		return "tar"
	case ".gz", ".bz2", ".xz", ".zst", ".lzma":
		return "compressed"
	case ".iso":
		return "iso"
	case ".001":
		return "split"
	}
	return ""
}

func bufReader(r io.Reader) *bufio.Reader { return bufio.NewReaderSize(r, 1<<20) }
