package arc

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/bzip2"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/flate"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	"golang.org/x/text/encoding/charmap"
)

// zent is one ZIP entry, from the central directory or (for damaged and .z01 sets) from scanning.
type zent struct {
	name     string // raw stored name, decoded to UTF-8
	flags    uint16
	method   uint16
	crc      uint32
	csize    int64
	usize    int64
	dataOff  int64
	modified time.Time
	extra    []byte
	mode     uint32 // external attributes (Unix in the high 16 bits when made on Unix)
	unix     bool
	dosTime  uint16
	dosDate  uint16
}

type zipArchive struct {
	pw      string
	src     *multiFile
	info    Info
	entries []*Entry
}

var zipMethods = map[uint16]string{0: "Store", 8: "Deflate", 9: "Deflate64", 12: "BZip2", 14: "LZMA", 93: "Zstandard", 95: "XZ", 98: "PPMd", 99: "AES"}

func openZip(src *multiFile, parts []string, o Options) (Archive, error) {
	r, err := zip.NewReader(src, src.size)
	if err != nil {
		// The list at the end is missing or broken (an unfinished download, a damaged file):
		// rebuild it from the file headers so the files can still be seen and saved.
		return openZipScan(src, parts, o, true)
	}
	a := &zipArchive{src: src, pw: o.Password, info: baseInfo("zip", src, parts)}
	a.info.Comment = r.Comment
	a.info.Editable = len(parts) == 1
	var ents []zent
	for _, f := range r.File {
		off, err := f.DataOffset()
		if err != nil {
			off = -1
		}
		ze := zent{name: zipName(f.Name, f.Flags, f.Extra), flags: f.Flags, method: f.Method, crc: f.CRC32,
			csize: int64(f.CompressedSize64), usize: int64(f.UncompressedSize64), dataOff: off,
			modified: f.Modified, extra: f.Extra, mode: f.ExternalAttrs, unix: f.CreatorVersion>>8 == 3,
			dosTime: f.ModifiedTime, dosDate: f.ModifiedDate}
		ents = append(ents, ze)
	}
	a.entries = zipEntries(ents)
	a.info.Encrypted = anyEncrypted(a.entries)
	if w := zipOverlap(ents); w != "" {
		a.info.Warning = w
	}
	return a, nil
}

func zipEntries(ents []zent) []*Entry {
	out := make([]*Entry, 0, len(ents))
	for i := range ents {
		ze := &ents[i]
		e := &Entry{raw: ze.name, Size: ze.usize, Packed: ze.csize, Modified: ze.modified, ref: ze,
			Encrypted: ze.flags&1 != 0, CRC: ze.crc, HasCRC: true}
		e.Dir = strings.HasSuffix(ze.name, "/") || strings.HasSuffix(ze.name, "\\")
		if ze.unix {
			e.Mode = unixMode(ze.mode >> 16)
			if e.Mode&modeType == modeSymlink {
				e.Link = "?"
			}
			if e.Mode.IsDir() {
				e.Dir = true
			}
		} else if ze.mode&0x10 != 0 {
			e.Dir = true
		}
		m := ze.method
		if m == 99 {
			if _, am, ok := aesExtra(ze.extra); ok {
				m = am
			}
			e.Method = zipMethods[m] + " · AES-256"
		} else {
			e.Method = zipMethods[m]
			if e.Encrypted {
				e.Method += " · ZipCrypto"
			}
		}
		if e.Method == "" {
			e.Method = "method " + itoa(int(m))
		}
		out = append(out, e)
	}
	finishEntries(out)
	return out
}

func anyEncrypted(es []*Entry) bool {
	for _, e := range es {
		if e.Encrypted {
			return true
		}
	}
	return false
}

// zipOverlap spots the "overlapping files" trick of non-recursive zip bombs (many entries sharing
// the same packed data, unpacking to petabytes).
func zipOverlap(ents []zent) string {
	type span struct{ a, b int64 }
	var s []span
	for _, e := range ents {
		if e.dataOff >= 0 && e.csize > 0 {
			s = append(s, span{e.dataOff, e.dataOff + e.csize})
		}
	}
	sort.Slice(s, func(i, j int) bool { return s[i].a < s[j].a })
	for i := 1; i < len(s); i++ {
		if s[i].a < s[i-1].b {
			return "Several files in this ZIP share the same data. That's a trick used by \"zip bombs\" to fill up a disk, so AnySave won't unpack it."
		}
	}
	return ""
}

// zipName decodes a stored name: UTF-8 when flagged or valid, the Info-ZIP Unicode extra field when
// present, else the old DOS code page (CP437) that Windows tools used.
func zipName(raw string, flags uint16, extra []byte) string {
	if flags&0x800 != 0 {
		return raw
	}
	for _, f := range extraFields(extra) {
		if f.tag == 0x7075 && len(f.data) > 5 && f.data[0] == 1 {
			if binary.LittleEndian.Uint32(f.data[1:5]) == crc32.ChecksumIEEE([]byte(raw)) && utf8.Valid(f.data[5:]) {
				return string(f.data[5:])
			}
		}
	}
	if utf8.ValidString(raw) {
		return raw
	}
	if s, err := charmap.CodePage437.NewDecoder().String(raw); err == nil {
		return s
	}
	return strings.ToValidUTF8(raw, "_")
}

type xfield struct {
	tag  uint16
	data []byte
}

func extraFields(b []byte) []xfield {
	var out []xfield
	for len(b) >= 4 {
		tag, n := binary.LittleEndian.Uint16(b), int(binary.LittleEndian.Uint16(b[2:]))
		b = b[4:]
		if n > len(b) {
			break
		}
		out = append(out, xfield{tag, b[:n]})
		b = b[n:]
	}
	return out
}

// aesExtra reads the WinZip AES field: strength (1–3) and the real packing method.
func aesExtra(extra []byte) (strength int, method uint16, ok bool) {
	for _, f := range extraFields(extra) {
		if f.tag == 0x9901 && len(f.data) >= 7 {
			return int(f.data[4]), binary.LittleEndian.Uint16(f.data[5:]), true
		}
	}
	return 0, 0, false
}

func (a *zipArchive) Info() Info        { return a.info }
func (a *zipArchive) Entries() []*Entry { return a.entries }
func (a *zipArchive) Close() error      { return a.src.Close() }

func (a *zipArchive) Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	return walkZip(ctx, a.src, a.entries, a.pw, want, fn)
}

func walkZip(ctx context.Context, src io.ReaderAt, entries []*Entry, pw string, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	for _, e := range entries {
		if ctx.Err() != nil {
			return ErrCancelled
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
		r, err := openZent(src, e.ref.(*zent), pw)
		if err != nil {
			return err
		}
		if err := fn(e, ctxReader{ctx, r}); err != nil {
			return stopOK(err)
		}
	}
	return nil
}

func stopOK(err error) error {
	if err == ErrStop {
		return nil
	}
	return err
}

// openZent gives the unpacked (and decrypted, checked) data of one entry.
func openZent(src io.ReaderAt, z *zent, pw string) (io.Reader, error) {
	if z.dataOff < 0 {
		return nil, damaged("the header of %s is broken", z.name)
	}
	raw := io.Reader(io.NewSectionReader(src, z.dataOff, z.csize))
	method := z.method
	checkCRC := true
	var auth *aesAuth
	if z.flags&1 != 0 {
		if pw == "" {
			return nil, ErrNeedPassword
		}
		if z.method == 99 {
			strength, am, ok := aesExtra(z.extra)
			if !ok {
				return nil, damaged("broken encryption details for %s", z.name)
			}
			r, au, err := aesReader(raw, z.csize, strength, pw)
			if err != nil {
				return nil, err
			}
			raw, auth, method = r, au, am
			vendor := 2
			for _, f := range extraFields(z.extra) {
				if f.tag == 0x9901 && len(f.data) >= 2 {
					vendor = int(binary.LittleEndian.Uint16(f.data))
				}
			}
			checkCRC = vendor == 1 && z.crc != 0 // AE-2 stores no CRC: the HMAC protects it instead
		} else {
			check := byte(z.crc >> 24)
			if z.flags&8 != 0 {
				check = byte(z.dosTime >> 8)
			}
			r, err := zipCryptoReader(raw, pw, check)
			if err != nil {
				return nil, err
			}
			raw = r
		}
	}
	dec, err := decompressor(method, raw)
	if err != nil {
		return nil, err
	}
	return &checkReader{r: dec, want: z.crc, check: checkCRC, size: z.usize, auth: auth, name: z.name}, nil
}

func decompressor(method uint16, r io.Reader) (io.Reader, error) {
	switch method {
	case 0:
		return r, nil
	case 8:
		return flate.NewReader(bufio.NewReaderSize(r, 256<<10)), nil
	case 12:
		return bzip2.NewReader(r), nil
	case 93:
		d, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(1<<31))
		if err != nil {
			return nil, err
		}
		return d.IOReadCloser(), nil
	case 95:
		x, err := xz.NewReader(r)
		if err != nil {
			return nil, damaged("broken XZ data")
		}
		return x, nil
	}
	name := zipMethods[method]
	if name == "" {
		name = "method " + itoa(int(method))
	}
	return nil, &UnsupportedError{Detail: "This ZIP uses a packing method AnySave can't read yet (" + name + ")."}
}

// checkReader stops at the stated size, and checks the CRC (and AES code) at the end.
type checkReader struct {
	r     io.Reader
	h     hash.Hash32
	want  uint32
	check bool
	size  int64
	n     int64
	auth  *aesAuth
	name  string
	done  bool
}

func (c *checkReader) Read(p []byte) (int, error) {
	if c.done {
		return 0, io.EOF
	}
	if c.h == nil {
		c.h = crc32.NewIEEE()
	}
	n, err := c.r.Read(p)
	c.h.Write(p[:n])
	c.n += int64(n)
	if c.size >= 0 && c.n > c.size {
		return n, damaged("%s is bigger than the archive says (a sign of tampering)", c.name)
	}
	if err == io.EOF {
		c.done = true
		if c.size >= 0 && c.n != c.size {
			return n, damaged("%s is cut short", c.name)
		}
		if c.auth != nil {
			if aerr := c.auth.verify(); aerr != nil {
				return n, aerr
			}
		}
		if c.check && c.h.Sum32() != c.want {
			return n, damaged("%s doesn't match its checksum", c.name)
		}
		return n, io.EOF
	}
	if err != nil {
		if err == io.ErrUnexpectedEOF || strings.Contains(err.Error(), "corrupt") || strings.Contains(err.Error(), "flate") {
			return n, damaged("%s is damaged", c.name)
		}
	}
	return n, err
}

// ---------- WinZip AES (AE-1 / AE-2) ----------

func aesSaltLen(strength int) int { return 4 + strength*4 } // 8, 12, 16
func aesKeyLen(strength int) int  { return 8 + strength*8 } // 16, 24, 32

type aesAuth struct {
	mac  hash.Hash
	code []byte
	src  io.Reader
}

func (a *aesAuth) verify() error {
	code := make([]byte, 10)
	if _, err := io.ReadFull(a.src, code); err != nil {
		return damaged("the encrypted data is cut short")
	}
	if subtle.ConstantTimeCompare(code, a.mac.Sum(nil)[:10]) != 1 {
		return damaged("the encrypted data was changed or is damaged")
	}
	return nil
}

func aesKeys(pw string, salt []byte, strength int) (key, macKey, verify []byte) {
	kl := aesKeyLen(strength)
	k, _ := pbkdf2.Key(sha1.New, pw, salt, 1000, 2*kl+2)
	return k[:kl], k[kl : 2*kl], k[2*kl:]
}

func aesReader(raw io.Reader, csize int64, strength int, pw string) (io.Reader, *aesAuth, error) {
	if strength < 1 || strength > 3 {
		return nil, nil, &UnsupportedError{Detail: "unknown AES strength"}
	}
	sl := aesSaltLen(strength)
	head := make([]byte, sl+2)
	if _, err := io.ReadFull(raw, head); err != nil {
		return nil, nil, damaged("the encrypted data is cut short")
	}
	key, macKey, verify := aesKeys(pw, head[:sl], strength)
	if subtle.ConstantTimeCompare(verify, head[sl:]) != 1 {
		return nil, nil, ErrWrongPassword
	}
	block, _ := aes.NewCipher(key)
	mac := hmac.New(sha1.New, macKey)
	dataLen := csize - int64(sl) - 2 - 10
	if dataLen < 0 {
		return nil, nil, damaged("the encrypted data is cut short")
	}
	body := io.LimitReader(raw, dataLen)
	r := &ctrReader{r: io.TeeReader(body, mac), s: newWzCTR(block)}
	return r, &aesAuth{mac: mac, src: raw}, nil
}

// wzCTR is AES in counter mode with WinZip's little-endian counter starting at 1.
type wzCTR struct {
	b   cipher.Block
	ctr [16]byte
	ks  [16]byte
	pos int
}

func newWzCTR(b cipher.Block) *wzCTR { return &wzCTR{b: b, pos: 16} }

func (c *wzCTR) xor(dst, src []byte) {
	for i := range src {
		if c.pos == 16 {
			for j := 0; j < 16; j++ {
				c.ctr[j]++
				if c.ctr[j] != 0 {
					break
				}
			}
			c.b.Encrypt(c.ks[:], c.ctr[:])
			c.pos = 0
		}
		dst[i] = src[i] ^ c.ks[c.pos]
		c.pos++
	}
}

type ctrReader struct {
	r io.Reader
	s *wzCTR
}

func (c *ctrReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.s.xor(p[:n], p[:n])
	return n, err
}

// ---------- old ZipCrypto (reading only: it's weak, so AnySave never makes it) ----------

type zcKeys [3]uint32

func (k *zcKeys) update(b byte) {
	k[0] = crc32.IEEETable[(k[0]^uint32(b))&0xff] ^ (k[0] >> 8)
	k[1] = (k[1]+(k[0]&0xff))*134775813 + 1
	k[2] = crc32.IEEETable[(k[2]^(k[1]>>24))&0xff] ^ (k[2] >> 8)
}

func (k *zcKeys) next() byte {
	t := uint16(k[2] | 2)
	return byte((uint32(t) * uint32(t^1)) >> 8)
}

func zipCryptoReader(raw io.Reader, pw string, check byte) (io.Reader, error) {
	k := &zcKeys{0x12345678, 0x23456789, 0x34567890}
	for i := 0; i < len(pw); i++ {
		k.update(pw[i])
	}
	head := make([]byte, 12)
	if _, err := io.ReadFull(raw, head); err != nil {
		return nil, damaged("the encrypted data is cut short")
	}
	for i := range head {
		head[i] ^= k.next()
		k.update(head[i])
	}
	if head[11] != check {
		return nil, ErrWrongPassword
	}
	return &zcReader{r: raw, k: k}, nil
}

type zcReader struct {
	r io.Reader
	k *zcKeys
}

func (z *zcReader) Read(p []byte) (int, error) {
	n, err := z.r.Read(p)
	for i := 0; i < n; i++ {
		p[i] ^= z.k.next()
		z.k.update(p[i])
	}
	return n, err
}

// ---------- end of central directory ----------

func hasZipEnd(src *multiFile) (bool, error) {
	n := int64(66000)
	if n > src.size {
		n = src.size
	}
	buf := make([]byte, n)
	if _, err := src.ReadAt(buf, src.size-n); err != nil && err != io.EOF {
		return false, err
	}
	return bytes.LastIndex(buf, []byte("PK\x05\x06")) >= 0, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b [20]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		p--
		b[p] = '-'
	}
	return string(b[p:])
}
