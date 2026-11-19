package arc

import (
	"context"
	"errors"
	"hash/crc32"
	"io"
	"strings"

	"github.com/bodgit/sevenzip"
)

var sig7z = []byte{'7', 'z', 0xBC, 0xAF, 0x27, 0x1C}

type sevenArchive struct {
	src     *multiFile
	r       *sevenzip.Reader
	info    Info
	entries []*Entry
}

func open7z(src *multiFile, parts []string, o Options) (Archive, error) {
	var r *sevenzip.Reader
	var err error
	if o.Password != "" {
		r, err = sevenzip.NewReaderWithPassword(src, src.size, o.Password)
	} else {
		r, err = sevenzip.NewReader(src, src.size)
	}
	info := baseInfo("7z", src, parts)
	if err != nil {
		var re *sevenzip.ReadError
		if errors.As(err, &re) && re.Encrypted {
			if o.Password == "" {
				return nil, ErrNeedPassword
			}
			return nil, ErrWrongPassword
		}
		return nil, damaged("%s", strings.TrimPrefix(err.Error(), "sevenzip: "))
	}
	a := &sevenArchive{src: src, r: r, info: info}
	a.info.NamesEncrypted = o.Password != "" && namesEncrypted7z(src)
	a.info.Editable = len(parts) == 1
	streams := map[int]int{}
	for _, f := range r.File {
		fi := f.FileInfo()
		e := &Entry{raw: f.Name, Size: int64(f.UncompressedSize), Packed: -1, Modified: f.Modified, ref: f,
			Dir: fi.IsDir(), Mode: fi.Mode(), CRC: f.CRC32, HasCRC: f.CRC32 != 0 && !fi.IsDir()}
		if fi.Mode()&modeSymlink != 0 {
			e.Link = "?"
		}
		if !e.Dir && f.UncompressedSize > 0 {
			streams[f.Stream]++
		}
		a.entries = append(a.entries, e)
	}
	for _, n := range streams {
		if n > 1 {
			a.info.Solid = true
		}
	}
	finishEntries(a.entries)
	enc := a.info.NamesEncrypted
	if !enc {
		enc = encrypted7z(src)
	}
	if enc {
		a.info.Encrypted = true
		for _, e := range a.entries {
			if !e.Dir && e.Size > 0 {
				e.Encrypted = true
			}
		}
	}
	return a, nil
}

// encrypted7z: read the start of the first file with two different passwords. Without encryption
// the password changes nothing; with it, the bytes differ (or the reading fails).
func encrypted7z(src *multiFile) bool {
	read := func(pw string) ([]byte, bool) {
		r, err := sevenzip.NewReaderWithPassword(src, src.size, pw)
		if err != nil {
			return nil, true
		}
		for _, f := range r.File {
			if f.UncompressedSize == 0 {
				continue
			}
			rc, err := f.Open()
			if err != nil {
				return nil, true
			}
			defer rc.Close()
			b := make([]byte, 64)
			n, err := io.ReadFull(rc, b)
			if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
				var re *sevenzip.ReadError
				return nil, errors.As(err, &re) && re.Encrypted
			}
			return b[:n], false
		}
		return nil, false
	}
	a, ea := read("\x00anysave-a")
	b, eb := read("\x00anysave-b")
	return ea || eb || string(a) != string(b)
}

// namesEncrypted7z is true when the list of names itself is encrypted (the 7z "encrypt file names").
func namesEncrypted7z(src *multiFile) bool {
	_, err := sevenzip.NewReader(src, src.size)
	var re *sevenzip.ReadError
	return err != nil && errors.As(err, &re) && re.Encrypted
}

func (a *sevenArchive) Info() Info        { return a.info }
func (a *sevenArchive) Entries() []*Entry { return a.entries }
func (a *sevenArchive) Close() error      { return a.src.Close() }

func (a *sevenArchive) Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	for _, e := range a.entries {
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
		f := e.ref.(*sevenzip.File)
		rc, err := f.Open()
		if err != nil {
			return seven7Err(err, a.info.Encrypted)
		}
		cr := &checkReader{r: sevenErrReader{rc, a.info.Encrypted}, want: f.CRC32, check: e.HasCRC, size: e.Size, name: e.Name}
		err = fn(e, ctxReader{ctx, cr})
		rc.Close()
		if err != nil {
			return stopOK(err)
		}
	}
	return nil
}

type sevenErrReader struct {
	r   io.Reader
	enc bool
}

func (s sevenErrReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if err != nil && err != io.EOF {
		return n, seven7Err(err, s.enc)
	}
	return n, err
}

func seven7Err(err error, enc bool) error {
	var re *sevenzip.ReadError
	if errors.As(err, &re) && re.Encrypted {
		if !enc {
			return ErrNeedPassword
		}
		return ErrWrongPassword
	}
	var d *DamagedError
	if errors.As(err, &d) || errors.Is(err, ErrCancelled) {
		return err
	}
	if strings.Contains(err.Error(), "unsupported") || strings.Contains(err.Error(), "unknown") {
		return &UnsupportedError{Detail: "This 7z uses a packing method AnySave can't read yet."}
	}
	return damaged("%s", strings.TrimPrefix(err.Error(), "sevenzip: "))
}

var _ = crc32.IEEE
