package arc

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/flate"
)

// ZIP files can be read without their central directory (the list at the end): every file also has
// a header in front of its data. This is how damaged ZIPs, unfinished downloads and old .z01 split
// sets are opened, and how "Repair" rebuilds a clean ZIP.

var (
	sigLocal = []byte("PK\x03\x04")
	sigDesc  = []byte("PK\x07\x08")
	sigCen   = []byte("PK\x01\x02")
)

func openZipScan(src *multiFile, parts []string, o Options, recovered bool) (Archive, error) {
	ents, err := scanZip(context.Background(), src, nil)
	if err != nil {
		return nil, err
	}
	if len(ents) == 0 {
		return nil, damaged("no files could be found in it")
	}
	a := &zipArchive{src: src, pw: o.Password, info: baseInfo("zip", src, parts)}
	a.info.Recovered = recovered
	a.entries = zipEntries(ents)
	a.info.Encrypted = anyEncrypted(a.entries)
	return a, nil
}

// scanZip finds every local file header and works out where its data ends.
func scanZip(ctx context.Context, src *multiFile, progress func(done, total int64)) ([]zent, error) {
	ents, _, err := scanZipBroken(ctx, src, progress)
	return ents, err
}

// scanZipBroken also names the files whose header was found but whose data can't be read.
func scanZipBroken(ctx context.Context, src *multiFile, progress func(done, total int64)) ([]zent, []string, error) {
	var out []zent
	var broken []string
	pos := int64(0)
	for pos < src.size {
		if ctx.Err() != nil {
			return nil, nil, ErrCancelled
		}
		off, cen := findSig(src, pos)
		if off < 0 || cen {
			break
		}
		z, end, ok := readLocal(src, off)
		if !ok {
			if z.name != "" && utf8.ValidString(z.name) && !strings.ContainsAny(z.name, "\x00\r\n") {
				broken = append(broken, z.name)
			}
			pos = off + 1
			continue
		}
		out = append(out, z)
		pos = end
		if progress != nil {
			progress(pos, src.size)
		}
	}
	return out, broken, nil
}

// findSig returns the next local header at or after pos (cen = the central directory came first).
func findSig(src *multiFile, pos int64) (int64, bool) {
	buf := make([]byte, 1<<20)
	for pos < src.size {
		n, _ := src.ReadAt(buf, pos)
		if n < 4 {
			return -1, false
		}
		b := buf[:n]
		i := bytes.Index(b, sigLocal)
		c := bytes.Index(b, sigCen)
		if c >= 0 && (i < 0 || c < i) {
			// Only trust a central directory entry that looks real (sane name length).
			if c+46 <= len(b) && binary.LittleEndian.Uint16(b[c+28:]) < 4096 {
				return pos + int64(c), true
			}
		}
		if i >= 0 {
			return pos + int64(i), false
		}
		pos += int64(n) - 3
	}
	return -1, false
}

func readLocal(src *multiFile, off int64) (zent, int64, bool) {
	var h [30]byte
	if _, err := src.ReadAt(h[:], off); err != nil {
		return zent{}, 0, false
	}
	le := binary.LittleEndian
	z := zent{flags: le.Uint16(h[6:]), method: le.Uint16(h[8:]), dosTime: le.Uint16(h[10:]), dosDate: le.Uint16(h[12:]),
		crc: le.Uint32(h[14:]), csize: int64(le.Uint32(h[18:])), usize: int64(le.Uint32(h[22:]))}
	nl, xl := int64(le.Uint16(h[26:])), int64(le.Uint16(h[28:]))
	if nl == 0 || nl > 4096 || z.method > 99 {
		return zent{}, 0, false
	}
	nx := make([]byte, nl+xl)
	if _, err := src.ReadAt(nx, off+30); err != nil {
		return zent{}, 0, false
	}
	z.extra = nx[nl:]
	z.name = zipName(string(nx[:nl]), z.flags, z.extra)
	z.modified = dosToTime(z.dosDate, z.dosTime)
	z.dataOff = off + 30 + nl + xl
	named := zent{name: z.name}
	zip64 := false
	for _, f := range extraFields(z.extra) {
		if f.tag == 0x0001 {
			zip64 = true
			d := f.data
			if z.usize == 0xffffffff && len(d) >= 8 {
				z.usize, d = int64(le.Uint64(d)), d[8:]
			}
			if z.csize == 0xffffffff && len(d) >= 8 {
				z.csize = int64(le.Uint64(d))
			}
		}
	}
	if z.flags&8 == 0 || (z.csize > 0 && z.csize != 0xffffffff) {
		end := z.dataOff + z.csize
		if end > src.size {
			return named, 0, false
		}
		if z.flags&8 != 0 {
			end = skipDesc(src, end, zip64, &z)
		}
		return z, end, true
	}
	// Sizes come after the data. Deflate data says where it ends; anything else: find the descriptor.
	if z.method == 8 && z.flags&1 == 0 {
		cr := &countByteReader{r: bufio.NewReaderSize(io.NewSectionReader(src, z.dataOff, src.size-z.dataOff), 64<<10)}
		fr := flate.NewReader(cr)
		n, err := io.Copy(io.Discard, fr)
		if err != nil {
			return named, 0, false
		}
		z.csize, z.usize = cr.n, n
		end := skipDesc(src, z.dataOff+z.csize, zip64, &z)
		return z, end, true
	}
	buf := make([]byte, 1<<20)
	for pos := z.dataOff; pos < src.size; {
		n, _ := src.ReadAt(buf, pos)
		if n < 16 {
			break
		}
		b := buf[:n]
		for i := 0; i+16 <= len(b); i++ {
			if !bytes.Equal(b[i:i+4], sigDesc) {
				continue
			}
			at := pos + int64(i)
			if int64(le.Uint32(b[i+8:])) == at-z.dataOff {
				z.crc, z.csize, z.usize = le.Uint32(b[i+4:]), at-z.dataOff, int64(le.Uint32(b[i+12:]))
				return z, at + 16, true
			}
			if i+24 <= len(b) && int64(le.Uint64(b[i+8:])) == at-z.dataOff {
				z.crc, z.csize, z.usize = le.Uint32(b[i+4:]), at-z.dataOff, int64(le.Uint64(b[i+16:]))
				return z, at + 24, true
			}
		}
		pos += int64(n) - 23
	}
	return named, 0, false
}

// skipDesc reads the data descriptor after the data (it may or may not start with its signature).
func skipDesc(src *multiFile, at int64, zip64 bool, z *zent) int64 {
	var b [24]byte
	n, _ := src.ReadAt(b[:], at)
	p := 0
	if n >= 4 && bytes.Equal(b[:4], sigDesc) {
		p = 4
	}
	le := binary.LittleEndian
	if zip64 {
		if n >= p+20 {
			z.crc = le.Uint32(b[p:])
			z.usize = int64(le.Uint64(b[p+12:]))
			return at + int64(p) + 20
		}
	} else if n >= p+12 {
		z.crc = le.Uint32(b[p:])
		if z.usize == 0 {
			z.usize = int64(le.Uint32(b[p+8:]))
		}
		return at + int64(p) + 12
	}
	return at
}

type countByteReader struct {
	r *bufio.Reader
	n int64
}

func (c *countByteReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countByteReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

func dosToTime(d, t uint16) time.Time {
	return time.Date(int(d>>9)+1980, time.Month(d>>5&0xf), int(d&0x1f), int(t>>11), int(t>>5&0x3f), int(t&0x1f)*2, 0, time.Local)
}

// RepairResult says what Repair kept.
type RepairResult struct {
	Kept    int      `json:"kept"`
	Dropped []string `json:"dropped,omitempty"` // damaged files left out
	Locked  int      `json:"locked"`            // encrypted files kept without checking (no password)
}

// RebuildZip writes a clean ZIP from the file headers of a damaged ZIP or a .z01 split set.
func RebuildZip(ctx context.Context, parts []string, out string, progress func(done, total int64)) error {
	_, err := RepairZip(ctx, parts, out, "", progress)
	return err
}

// RepairZip rebuilds a ZIP, checking every file and leaving out those that are damaged.
func RepairZip(ctx context.Context, parts []string, out, pw string, progress func(done, total int64)) (*RepairResult, error) {
	src, err := openMulti(parts)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	ents, broken, err := scanZipBroken(ctx, src, func(d, t int64) {
		if progress != nil {
			progress(d/2, t)
		}
	})
	if err != nil {
		return nil, err
	}
	if len(ents) == 0 {
		return nil, damaged("no files could be found in it")
	}
	f, err := os.Create(out)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(out)
		}
	}()
	bw := bufio.NewWriterSize(f, 1<<20)
	w := zip.NewWriter(bw)
	res := &RepairResult{Dropped: broken}
	for i := range ents {
		if ctx.Err() != nil {
			return nil, ErrCancelled
		}
		z := &ents[i]
		isDir := len(z.name) > 0 && (z.name[len(z.name)-1] == '/')
		if !isDir {
			if z.flags&1 != 0 && pw == "" {
				res.Locked++
			} else if r, err := openZent(src, z, pw); err != nil {
				if errors.Is(err, ErrWrongPassword) {
					return nil, err
				}
				res.Dropped = append(res.Dropped, z.name)
				continue
			} else if _, err := io.Copy(io.Discard, r); err != nil {
				res.Dropped = append(res.Dropped, z.name)
				continue
			}
		}
		fh := &zip.FileHeader{Name: z.name, Flags: keepDescFlag(z), Method: z.method, CRC32: z.crc,
			CompressedSize64: uint64(z.csize), UncompressedSize64: uint64(z.usize), ModifiedTime: z.dosTime, ModifiedDate: z.dosDate,
			Extra: stripZip64(z.extra)}
		fh.Flags |= 0x800 // names are UTF-8 now
		dw, err := w.CreateRaw(fh)
		if err != nil {
			return nil, err
		}
		if !isDir {
			if _, err := io.Copy(dw, io.NewSectionReader(src, z.dataOff, z.csize)); err != nil {
				return nil, err
			}
		}
		res.Kept++
		if progress != nil {
			progress(src.size/2+z.dataOff/2, src.size)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	if err := bw.Flush(); err != nil {
		return nil, err
	}
	ok = true
	return res, nil
}

// keepDescFlag drops the "sizes come after the data" flag (sizes are known now), except for old
// ZipCrypto entries, whose password check byte depends on that flag.
func keepDescFlag(z *zent) uint16 {
	if z.flags&1 != 0 && z.method != 99 {
		return z.flags
	}
	return z.flags &^ 8
}

func stripZip64(extra []byte) []byte {
	var out []byte
	for _, f := range extraFields(extra) {
		if f.tag == 0x0001 || f.tag == 0x7075 {
			continue
		}
		var h [4]byte
		binary.LittleEndian.PutUint16(h[:], f.tag)
		binary.LittleEndian.PutUint16(h[2:], uint16(len(f.data)))
		out = append(append(out, h[:]...), f.data...)
	}
	return out
}

// RepairOut is the name for a repaired copy: "name (repaired).zip".
func RepairOut(dir, name string) string {
	ext := filepath.Ext(name)
	return uniquePath(filepath.Join(dir, name[:len(name)-len(ext)]+" (repaired)"+ext))
}
