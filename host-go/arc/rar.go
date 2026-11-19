package arc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// RAR is read only (RAR 1.5 to RAR5, locked and split ones too) with rardecode (BSD-2 licence).
// RARLAB's licence doesn't allow making RAR files, so AnySave never does.

var (
	rarSig4 = []byte("Rar!\x1a\x07\x00")
	rarSig5 = []byte("Rar!\x1a\x07\x01\x00")
)

type rarArchive struct {
	first   string
	pw      string
	info    Info
	entries []*Entry
}

func openRar(first string, parts []string, o Options) (Archive, error) {
	var opts []rardecode.Option
	if o.Password != "" {
		opts = append(opts, rardecode.Password(o.Password))
	}
	files, err := rardecode.List(first, opts...)
	if err != nil && len(files) == 0 {
		if o.Password != "" { // RAR4 can't check a password: a header-locked one just reads as garbage
			if _, e2 := rardecode.List(first); errors.Is(e2, rardecode.ErrArchiveEncrypted) {
				return nil, ErrWrongPassword
			}
		}
		return nil, rarErr(err, o.Password != "", false)
	}
	a := &rarArchive{first: first, pw: o.Password}
	a.info = Info{Format: "rar", Version: "RAR4"}
	if head, _ := readHead(first, 8); bytes.HasPrefix(head, rarSig5) {
		a.info.Version = "RAR5"
	}
	for _, p := range parts {
		if st, err := os.Stat(p); err == nil {
			a.info.Size += st.Size()
		}
	}
	if len(parts) > 1 {
		a.info.Volumes = parts
	}
	for _, f := range files {
		h := f.FileHeader
		e := &Entry{raw: h.Name, Size: h.UnPackedSize, Packed: h.PackedSize, Modified: h.ModificationTime,
			Dir: h.IsDir, Encrypted: h.Encrypted, Mode: h.Mode()}
		if h.UnKnownSize {
			e.Size = -1
		}
		if h.LinkType != rardecode.LinkTypeNone {
			e.Link = h.LinkTarget
			if e.Link == "" {
				e.Link = "?"
			}
			e.Dir = false
		}
		if h.Solid {
			a.info.Solid = true
		}
		if h.Encrypted {
			a.info.Encrypted = true
		}
		if h.HeaderEncrypted {
			a.info.NamesEncrypted = true
		}
		a.entries = append(a.entries, e)
	}
	finishEntries(a.entries)
	if err != nil { // a damaged tail: show what could be read
		a.info.Recovered = true
	}
	return a, nil
}

func readHead(p string, n int) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	m, _ := io.ReadFull(f, b)
	return b[:m], nil
}

func rarErr(err error, havePw, encrypted bool) error {
	switch {
	case errors.Is(err, rardecode.ErrArchiveEncrypted), errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		if havePw {
			return ErrWrongPassword
		}
		return ErrNeedPassword
	case errors.Is(err, rardecode.ErrBadPassword):
		return ErrWrongPassword
	case errors.Is(err, ErrCancelled):
		return err
	case errors.Is(err, rardecode.ErrMultiVolume), errors.Is(err, os.ErrNotExist):
		return damaged("a part of this split RAR is missing")
	case errors.Is(err, rardecode.ErrUnknownVersion):
		return &UnsupportedError{Detail: "This RAR is a version AnySave can't read."}
	}
	var d *DamagedError
	if errors.As(err, &d) {
		return err
	}
	if encrypted && havePw && (errors.Is(err, rardecode.ErrBadFileChecksum) || errors.Is(err, rardecode.ErrBadHeaderCRC)) {
		return ErrWrongPassword
	}
	return damaged("%s", strings.TrimPrefix(err.Error(), "rardecode: "))
}

func (a *rarArchive) Info() Info        { return a.info }
func (a *rarArchive) Entries() []*Entry { return a.entries }
func (a *rarArchive) Close() error      { return nil }

func (a *rarArchive) Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	any := false
	for _, e := range a.entries {
		if want(e) {
			any = true
			break
		}
	}
	if !any {
		return nil
	}
	var opts []rardecode.Option
	if a.pw != "" {
		opts = append(opts, rardecode.Password(a.pw))
	}
	r, err := rardecode.OpenReader(a.first, opts...)
	if err != nil {
		return rarErr(err, a.pw != "", a.info.Encrypted)
	}
	defer r.Close()
	byName := map[string][]*Entry{}
	for _, e := range a.entries {
		byName[e.raw] = append(byName[e.raw], e)
	}
	i := 0
	for {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		h, err := r.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return rarErr(err, a.pw != "", a.info.Encrypted)
		}
		var e *Entry
		if i < len(a.entries) && a.entries[i].raw == h.Name {
			e = a.entries[i]
		} else if l := byName[h.Name]; len(l) > 0 {
			e = l[0]
		}
		i++
		if e == nil {
			continue
		}
		if l := byName[h.Name]; len(l) > 0 && l[0] == e {
			byName[h.Name] = l[1:]
		}
		if !want(e) {
			continue
		}
		if e.Dir || e.Special || e.Unsafe != "" {
			if err := fn(e, nil); err != nil {
				return stopOK(err)
			}
			continue
		}
		enc := h.Encrypted
		er := rarReader{r: r, havePw: a.pw != "", enc: enc}
		var src io.Reader = er
		if e.Size >= 0 {
			src = &sizeGuard{r: er, max: e.Size, name: e.Name}
		}
		if err := fn(e, ctxReader{ctx, src}); err != nil {
			return stopOK(err)
		}
	}
}

type rarReader struct {
	r      io.Reader
	havePw bool
	enc    bool
}

func (r rarReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if err != nil && err != io.EOF {
		if !r.havePw && r.enc {
			return n, ErrNeedPassword
		}
		return n, rarErr(err, r.havePw, r.enc)
	}
	return n, err
}

// sizeGuard fails a file that turns out bigger than its header says (a zip-bomb style lie).
type sizeGuard struct {
	r    io.Reader
	max  int64
	n    int64
	name string
}

func (s *sizeGuard) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.n += int64(n)
	if s.n > s.max {
		return n, damaged("%s is bigger than the archive says (a sign of tampering)", s.name)
	}
	return n, err
}
