package arc

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"hash/crc32"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ulikunitz/xz/lzma"
)

// A 7z writer in pure Go: LZMA2 packing (or plain storing), 7-Zip's AES-256 encryption, encrypted
// (and packed) file lists, and solid blocks. The layout follows 7-Zip's 7zFormat.txt.

func newAES(k []byte) (cipher.Block, error) { return aes.NewCipher(k) }

const (
	k7End           = 0x00
	k7Header        = 0x01
	k7MainStreams   = 0x04
	k7FilesInfo     = 0x05
	k7PackInfo      = 0x06
	k7UnpackInfo    = 0x07
	k7SubStreams    = 0x08
	k7Size          = 0x09
	k7CRC           = 0x0A
	k7Folder        = 0x0B
	k7CodersUnpack  = 0x0C
	k7NumUnpack     = 0x0D
	k7EmptyStream   = 0x0E
	k7EmptyFile     = 0x0F
	k7Name          = 0x11
	k7MTime         = 0x14
	k7WinAttributes = 0x15
	k7EncodedHeader = 0x17
	solidBlockBytes = 512 << 20
	sevenAESCycles  = 19
)

var (
	coderCopy  = []byte{0x00}
	coderLZMA2 = []byte{0x21}
	coderAES   = []byte{0x06, 0xF1, 0x07, 0x01}
)

type sevenFile struct {
	name   string
	dir    bool
	size   int64
	crc    uint32
	mtime  time.Time
	attrib uint32
}

type sevenCoder struct {
	id    []byte
	props []byte
}

type sevenFolder struct {
	coders       []sevenCoder
	unpack       []int64 // per coder output, in coder order
	packSize     int64
	sizes        []int64 // the files (sub-streams) inside
	crcs         []uint32
	folderCRC    uint32
	hasFolderCRC bool
}

type sevenWriter struct {
	v       *VolWriter
	o       CreateOptions
	files   []sevenFile
	folders []*sevenFolder
	cur     *folderEnc
	pos     int64 // bytes written after the 32-byte start header
	key     []byte
	err     error
	small   bool // packing the list: a small dictionary is plenty
}

func newSevenWriter(v *VolWriter, o CreateOptions) (*sevenWriter, error) {
	w := &sevenWriter{v: v, o: o}
	if o.Password != "" {
		w.key = sevenKey(o.Password, sevenAESCycles)
	}
	if _, err := v.Write(make([]byte, 32)); err != nil { // the start header, filled in at the end
		return nil, err
	}
	return w, nil
}

// sevenKey is 7-Zip's key: SHA-256 over (password as UTF-16LE + 8-byte counter), 2^cycles times.
func sevenKey(pw string, cycles int) []byte {
	u := utf16.Encode([]rune(pw))
	b := make([]byte, len(u)*2+8)
	for i, c := range u {
		binary.LittleEndian.PutUint16(b[i*2:], c)
	}
	h := sha256.New()
	n := uint64(1) << cycles
	ctr := b[len(u)*2:]
	for i := uint64(0); i < n; i++ {
		binary.LittleEndian.PutUint64(ctr, i)
		h.Write(b)
	}
	return h.Sum(nil)
}

// folderEnc is one packed stream being written: data → LZMA2 (or copy) → AES (optional) → disk.
type folderEnc struct {
	f        *sevenFolder
	in       io.WriteCloser // what file data is written into
	lz       *countW        // counts the LZMA2 output (= AES input)
	out      *countW        // counts bytes on disk
	aesw     *cbcWriter
	unpacked int64
}

func (w *sevenWriter) lzmaConfig() (lzma.Writer2Config, int) {
	dict := map[int]int{1: 1 << 20, 2: 2 << 20, 3: 4 << 20, 4: 8 << 20, 5: 16 << 20, 6: 16 << 20, 7: 32 << 20, 8: 64 << 20, 9: 64 << 20}[w.o.Level]
	if dict == 0 {
		dict = 16 << 20
	}
	if w.small {
		dict = 1 << 20
	}
	return lzma.Writer2Config{DictCap: dict, Matcher: lzma.HashTable4}, dict
}

func (w *sevenWriter) startFolder(store bool) (*folderEnc, error) {
	f := &sevenFolder{}
	fe := &folderEnc{f: f}
	fe.out = &countW{w: w.v}
	var sink io.Writer = fe.out
	if w.key != nil {
		iv := make([]byte, 16)
		rand.Read(iv)
		block, _ := aes.NewCipher(w.key)
		fe.aesw = &cbcWriter{w: fe.out, m: cipher.NewCBCEncrypter(block, iv)}
		sink = fe.aesw
		// 7-Zip lists AES first: it's the first step when unpacking.
		f.coders = append(f.coders, sevenCoder{coderAES, append([]byte{sevenAESCycles | 0x40, 0x0F}, iv...)})
	}
	fe.lz = &countW{w: sink}
	if store || w.o.Level == 0 {
		f.coders = append(f.coders, sevenCoder{id: coderCopy})
		fe.in = nopCloser{fe.lz}
		return fe, nil
	}
	cfg, dict := w.lzmaConfig()
	lw, err := cfg.NewWriter2(fe.lz)
	if err != nil {
		return nil, err
	}
	f.coders = append(f.coders, sevenCoder{coderLZMA2, []byte{lzma2DictByte(uint32(dict))}})
	fe.in = lw
	return fe, nil
}

func (w *sevenWriter) finishFolder(fe *folderEnc) error {
	if err := fe.in.Close(); err != nil {
		return err
	}
	if fe.aesw != nil {
		if err := fe.aesw.Close(); err != nil {
			return err
		}
	}
	f := fe.f
	f.unpack = []int64{fe.unpacked}
	if len(f.coders) == 2 { // [AES, packer]: AES gives the packed bytes, the packer the files
		f.unpack = []int64{fe.lz.n, fe.unpacked}
	}
	f.packSize = fe.out.n
	w.pos += fe.out.n
	w.folders = append(w.folders, f)
	return nil
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

func (w *sevenWriter) Add(h FileHeader, r io.Reader) error {
	if w.err != nil {
		return w.err
	}
	name := strings.Trim(h.Name, "/")
	sf := sevenFile{name: name, dir: h.Dir, mtime: h.Modified}
	mode := uint32(h.Mode.Perm())
	if h.Dir {
		if mode == 0 {
			mode = 0o755
		}
		sf.attrib = 0x10 | 0x8000 | (0x4000|mode)<<16
	} else {
		if mode == 0 {
			mode = 0o644
		}
		sf.attrib = 0x20 | 0x8000 | (0x8000|mode)<<16
	}
	if h.Dir || r == nil {
		w.files = append(w.files, sf)
		return nil
	}
	store := packedExt[strings.ToLower(path.Ext(name))] && !w.o.Solid
	if w.cur == nil || !w.o.Solid {
		fe, err := w.startFolder(store)
		if err != nil {
			w.err = err
			return err
		}
		w.cur = fe
	}
	crc := crc32.NewIEEE()
	n, err := io.Copy(io.MultiWriter(w.cur.in, crc), r)
	if err != nil {
		w.err = err
		return err
	}
	sf.size, sf.crc = n, crc.Sum32()
	w.files = append(w.files, sf)
	if n == 0 { // empty file: no stream at all
		if !w.o.Solid && w.cur != nil && w.cur.unpacked == 0 {
			w.cur = nil // nothing written into this folder yet: drop it (its encoder holds no data)
		}
		return nil
	}
	w.cur.unpacked += n
	w.cur.f.sizes = append(w.cur.f.sizes, n)
	w.cur.f.crcs = append(w.cur.f.crcs, sf.crc)
	if !w.o.Solid || w.cur.unpacked >= solidBlockBytes {
		if err := w.finishFolder(w.cur); err != nil {
			w.err = err
			return err
		}
		w.cur = nil
	}
	return nil
}

func (w *sevenWriter) Close() error {
	if w.err != nil {
		return w.err
	}
	if w.cur != nil && w.cur.unpacked > 0 {
		if err := w.finishFolder(w.cur); err != nil {
			return err
		}
	}
	w.cur = nil
	header := w.header()
	next := header
	if w.key != nil && w.o.EncryptNames || len(header) > 4096 {
		// Pack (and lock) the list itself, as 7-Zip does.
		key := w.key
		if !w.o.EncryptNames {
			w.key = nil // packed but not locked
		}
		w.small = true
		fe, err := w.startFolder(false)
		w.key, w.small = key, false
		if err != nil {
			return err
		}
		start := w.pos
		if _, err := fe.in.Write(header); err != nil {
			return err
		}
		fe.unpacked = int64(len(header))
		if err := w.finishFolder(fe); err != nil {
			return err
		}
		hf := w.folders[len(w.folders)-1]
		w.folders = w.folders[:len(w.folders)-1]
		hf.folderCRC, hf.hasFolderCRC = crc32.ChecksumIEEE(header), true
		var b bytes.Buffer
		b.WriteByte(k7EncodedHeader)
		writePackInfo(&b, start, []*sevenFolder{hf})
		writeUnpackInfo(&b, []*sevenFolder{hf})
		b.WriteByte(k7End)
		next = b.Bytes()
	}
	if _, err := w.v.Write(next); err != nil {
		return err
	}
	var sh [32]byte
	copy(sh[:], sig7z)
	sh[6], sh[7] = 0, 4
	binary.LittleEndian.PutUint64(sh[12:], uint64(w.pos))
	binary.LittleEndian.PutUint64(sh[20:], uint64(len(next)))
	binary.LittleEndian.PutUint32(sh[28:], crc32.ChecksumIEEE(next))
	binary.LittleEndian.PutUint32(sh[8:], crc32.ChecksumIEEE(sh[12:32]))
	_, err := w.v.WriteAt(sh[:], 0)
	return err
}

// ---------- header ----------

func (w *sevenWriter) header() []byte {
	var b bytes.Buffer
	b.WriteByte(k7Header)
	if len(w.folders) > 0 {
		b.WriteByte(k7MainStreams)
		writePackInfo(&b, 0, w.folders)
		writeUnpackInfo(&b, w.folders)
		writeSubStreams(&b, w.folders)
		b.WriteByte(k7End)
	}
	if len(w.files) > 0 {
		w.writeFiles(&b)
	}
	b.WriteByte(k7End)
	return b.Bytes()
}

func writePackInfo(b *bytes.Buffer, packPos int64, fs []*sevenFolder) {
	b.WriteByte(k7PackInfo)
	writeNum(b, uint64(packPos))
	writeNum(b, uint64(len(fs)))
	b.WriteByte(k7Size)
	for _, f := range fs {
		writeNum(b, uint64(f.packSize))
	}
	b.WriteByte(k7End)
}

func writeUnpackInfo(b *bytes.Buffer, fs []*sevenFolder) {
	b.WriteByte(k7UnpackInfo)
	b.WriteByte(k7Folder)
	writeNum(b, uint64(len(fs)))
	b.WriteByte(0) // not external
	for _, f := range fs {
		writeNum(b, uint64(len(f.coders)))
		for _, c := range f.coders {
			flag := byte(len(c.id))
			if len(c.props) > 0 {
				flag |= 0x20
			}
			b.WriteByte(flag)
			b.Write(c.id)
			if len(c.props) > 0 {
				writeNum(b, uint64(len(c.props)))
				b.Write(c.props)
			}
		}
		if len(f.coders) == 2 { // bind pair: the packer's input (in 1) is AES's output (out 0)
			writeNum(b, 1)
			writeNum(b, 0)
		}
	}
	b.WriteByte(k7CodersUnpack)
	for _, f := range fs {
		for _, s := range f.unpack {
			writeNum(b, uint64(s))
		}
	}
	if fs[0].hasFolderCRC {
		b.WriteByte(k7CRC)
		b.WriteByte(1)
		for _, f := range fs {
			binary.Write(b, binary.LittleEndian, f.folderCRC)
		}
	}
	b.WriteByte(k7End)
}

func writeSubStreams(b *bytes.Buffer, fs []*sevenFolder) {
	b.WriteByte(k7SubStreams)
	multi := false
	for _, f := range fs {
		if len(f.sizes) != 1 {
			multi = true
		}
	}
	if multi {
		b.WriteByte(k7NumUnpack)
		for _, f := range fs {
			writeNum(b, uint64(len(f.sizes)))
		}
		b.WriteByte(k7Size)
		for _, f := range fs {
			for i := 0; i+1 < len(f.sizes); i++ {
				writeNum(b, uint64(f.sizes[i]))
			}
		}
	}
	b.WriteByte(k7CRC)
	b.WriteByte(1) // all defined
	for _, f := range fs {
		for _, c := range f.crcs {
			binary.Write(b, binary.LittleEndian, c)
		}
	}
	b.WriteByte(k7End)
}

func (w *sevenWriter) writeFiles(b *bytes.Buffer) {
	n := len(w.files)
	b.WriteByte(k7FilesInfo)
	writeNum(b, uint64(n))
	empty := make([]bool, n)
	var emptyFile []bool
	anyEmpty, anyEmptyFile := false, false
	for i, f := range w.files {
		if f.dir || f.size == 0 {
			empty[i] = true
			anyEmpty = true
			emptyFile = append(emptyFile, !f.dir)
			if !f.dir {
				anyEmptyFile = true
			}
		}
	}
	if anyEmpty {
		v := bitVector(empty)
		b.WriteByte(k7EmptyStream)
		writeNum(b, uint64(len(v)))
		b.Write(v)
		if anyEmptyFile {
			v := bitVector(emptyFile)
			b.WriteByte(k7EmptyFile)
			writeNum(b, uint64(len(v)))
			b.Write(v)
		}
	}
	var names bytes.Buffer
	names.WriteByte(0) // not external
	for _, f := range w.files {
		for _, c := range utf16.Encode([]rune(f.name)) {
			binary.Write(&names, binary.LittleEndian, c)
		}
		names.Write([]byte{0, 0})
	}
	b.WriteByte(k7Name)
	writeNum(b, uint64(names.Len()))
	b.Write(names.Bytes())

	b.WriteByte(k7MTime)
	writeNum(b, uint64(2+8*n))
	b.WriteByte(1) // all defined
	b.WriteByte(0) // not external
	for _, f := range w.files {
		t := f.mtime
		if t.IsZero() {
			t = time.Now()
		}
		binary.Write(b, binary.LittleEndian, uint64(t.UnixNano()/100+116444736000000000))
	}
	b.WriteByte(k7WinAttributes)
	writeNum(b, uint64(2+4*n))
	b.WriteByte(1)
	b.WriteByte(0)
	for _, f := range w.files {
		binary.Write(b, binary.LittleEndian, f.attrib)
	}
	b.WriteByte(k7End)
}

func bitVector(bits []bool) []byte {
	v := make([]byte, (len(bits)+7)/8)
	for i, on := range bits {
		if on {
			v[i/8] |= 0x80 >> (i % 8)
		}
	}
	return v
}

// writeNum is 7z's variable-length number (7-Zip's WriteNumber).
func writeNum(b *bytes.Buffer, v uint64) {
	first := byte(0)
	mask := byte(0x80)
	i := 0
	for ; i < 8; i++ {
		if v < uint64(1)<<(7*(i+1)) {
			first |= byte(v >> (8 * i))
			break
		}
		first |= mask
		mask >>= 1
	}
	b.WriteByte(first)
	for ; i > 0; i-- {
		b.WriteByte(byte(v))
		v >>= 8
	}
}

func lzma2DictByte(d uint32) byte {
	for p := 0; p < 40; p++ {
		if (uint64(2|(p&1)) << (p/2 + 11)) >= uint64(d) {
			return byte(p)
		}
	}
	return 40
}

// cbcWriter encrypts with AES-CBC, padding the end with zeros (7-Zip's way).
type cbcWriter struct {
	w   io.Writer
	m   cipher.BlockMode
	buf []byte
	out []byte
}

func (c *cbcWriter) Write(p []byte) (int, error) {
	n := len(p)
	c.buf = append(c.buf, p...)
	full := len(c.buf) / 16 * 16
	if full >= 64<<10 || (full > 0 && len(c.buf) > 64<<10) {
		if err := c.flush(full); err != nil {
			return 0, err
		}
	}
	return n, nil
}

func (c *cbcWriter) flush(full int) error {
	if full == 0 {
		return nil
	}
	if cap(c.out) < full {
		c.out = make([]byte, full)
	}
	out := c.out[:full]
	c.m.CryptBlocks(out, c.buf[:full])
	if _, err := c.w.Write(out); err != nil {
		return err
	}
	c.buf = append(c.buf[:0], c.buf[full:]...)
	return nil
}

func (c *cbcWriter) Close() error {
	if r := len(c.buf) % 16; r != 0 {
		c.buf = append(c.buf, make([]byte, 16-r)...)
	}
	return c.flush(len(c.buf))
}

var _ hash.Hash = nil
