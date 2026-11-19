package arc

import (
	"archive/tar"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"encoding/binary"
	"io"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
)

func compressionOf(head []byte) string {
	switch {
	case bytes.HasPrefix(head, []byte{0x1f, 0x8b}):
		return "gz"
	case bytes.HasPrefix(head, []byte("BZh")) && len(head) > 3 && head[3] >= '1' && head[3] <= '9':
		return "bz2"
	case bytes.HasPrefix(head, []byte{0xFD, '7', 'z', 'X', 'Z', 0}):
		return "xz"
	case bytes.HasPrefix(head, []byte{0x28, 0xB5, 0x2F, 0xFD}):
		return "zst"
	}
	return ""
}

func decompress(c string, r io.Reader) (io.Reader, func(), error) {
	switch c {
	case "gz":
		g, err := gzip.NewReader(r)
		if err != nil {
			return nil, nil, damaged("broken GZ data")
		}
		return g, func() { g.Close() }, nil
	case "bz2":
		return bzip2.NewReader(r), func() {}, nil
	case "xz":
		x, err := xz.NewReader(r)
		if err != nil {
			return nil, nil, damaged("broken XZ data")
		}
		return x, func() {}, nil
	case "zst":
		d, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1<<31))
		if err != nil {
			return nil, nil, damaged("broken Zstandard data")
		}
		return d, d.Close, nil
	}
	return r, func() {}, nil
}

func isTarHeader(b []byte) bool {
	if len(b) < 512 {
		return false
	}
	if bytes.Equal(b[257:262], []byte("ustar")) {
		return true
	}
	// Old V7 tar: check the header checksum.
	field := strings.Trim(string(b[148:156]), " \x00")
	want, err := strconv.ParseInt(field, 8, 64)
	if err != nil || field == "" {
		return false
	}
	var sum int64
	for i, c := range b[:512] {
		if i >= 148 && i < 156 {
			c = ' '
		}
		sum += int64(c)
	}
	return sum == want && b[0] != 0
}

type tarArchive struct {
	src     *multiFile
	comp    string
	info    Info
	entries []*Entry
	single  string // a single compressed file (not a tar): its name
}

func (a *tarArchive) open() (io.Reader, func(), error) {
	return decompress(a.comp, bufReader(a.src.reader()))
}

func openCompressed(src *multiFile, parts []string, c, lname string) (Archive, error) {
	a := &tarArchive{src: src, comp: c}
	r, done, err := a.open()
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(r, head)
	done()
	if n == 512 && isTarHeader(head) {
		return openTar(src, parts, c)
	}
	a.info = baseInfo(c, src, parts)
	name := strings.TrimSuffix(filepath.Base(SetName(lname)), filepath.Ext(lname))
	if g, ok := r.(*gzip.Reader); ok && g.Name != "" {
		name = filepath.Base(g.Name)
	}
	_, orig := cleanName(name)
	if name == "" || orig != "" {
		name = "file"
	}
	size := int64(-1)
	if c == "gz" && src.size >= 18 { // gzip keeps the size (mod 4 GB) in its last 4 bytes
		var b [4]byte
		if _, err := src.ReadAt(b[:], src.size-4); err == nil {
			if s := int64(binary.LittleEndian.Uint32(b[:])); s >= int64(n) && src.size < 1<<32 {
				size = s
			}
		}
	}
	a.single = name
	a.entries = []*Entry{{raw: name, Size: size, Packed: src.size, Method: strings.ToUpper(c)}}
	finishEntries(a.entries)
	return a, nil
}

func openTar(src *multiFile, parts []string, c string) (Archive, error) {
	a := &tarArchive{src: src, comp: c}
	a.info = baseInfo("tar", src, parts)
	if c != "" {
		a.info.Format = "tar." + c
	}
	r, done, err := a.open()
	if err != nil {
		return nil, err
	}
	defer done()
	t := tar.NewReader(r)
	for {
		h, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			if len(a.entries) == 0 {
				return nil, damaged("the TAR data is broken")
			}
			a.info.Recovered = true // cut short: show what's there
			break
		}
		a.entries = append(a.entries, tarEntry(h))
	}
	finishEntries(a.entries)
	return a, nil
}

func tarEntry(h *tar.Header) *Entry {
	e := &Entry{raw: h.Name, Size: h.Size, Packed: -1, Modified: h.ModTime, Mode: h.FileInfo().Mode()}
	switch h.Typeflag {
	case tar.TypeDir:
		e.Dir, e.Size = true, 0
	case tar.TypeReg, tar.TypeRegA, tar.TypeGNUSparse:
	case tar.TypeSymlink, tar.TypeLink:
		e.Link = h.Linkname
		if e.Link == "" {
			e.Link = "?"
		}
		e.Size = 0
	default:
		e.Special, e.Size = true, 0
		e.Link = "special"
	}
	return e
}

func (a *tarArchive) Info() Info        { return a.info }
func (a *tarArchive) Entries() []*Entry { return a.entries }
func (a *tarArchive) Close() error      { return a.src.Close() }

func (a *tarArchive) Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	r, done, err := a.open()
	if err != nil {
		return err
	}
	defer done()
	if a.single != "" {
		e := a.entries[0]
		if !want(e) {
			return nil
		}
		var src io.Reader = &tarDataErr{r}
		return stopOK(fn(e, ctxReader{ctx, src}))
	}
	t := tar.NewReader(r)
	for i := 0; ; i++ {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		_, err := t.Next()
		if err == io.EOF || (err != nil && i >= len(a.entries) && a.info.Recovered) {
			return nil
		}
		if err != nil {
			return damaged("the TAR data is broken")
		}
		if i >= len(a.entries) {
			return nil
		}
		e := a.entries[i]
		if !want(e) {
			continue
		}
		if e.Dir || e.Special || e.Link != "" || e.Unsafe != "" {
			if err := fn(e, nil); err != nil {
				return stopOK(err)
			}
			continue
		}
		if err := fn(e, ctxReader{ctx, &tarDataErr{t}}); err != nil {
			return stopOK(err)
		}
	}
}

type tarDataErr struct{ r io.Reader }

func (t *tarDataErr) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && err != ErrCancelled {
		return n, damaged("the data is broken or cut short")
	}
	return n, err
}
