package main

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/bodgit/sevenzip"
	"github.com/ulikunitz/xz"
)

// Archives: unpack ZIP, 7z (also locked ones), TAR and GZ/BZ2/XZ; pack ZIP or TAR.GZ.
// RAR can't be unpacked (its maker doesn't allow free unpackers to be shipped with apps like this).

var doubleExt = []string{".tar.gz", ".tar.bz2", ".tar.xz"}

func archiveBase(name string) string {
	l := strings.ToLower(name)
	for _, e := range doubleExt {
		if strings.HasSuffix(l, e) {
			return name[:len(name)-len(e)]
		}
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

func isTarName(l string) bool {
	return strings.Contains(l, ".tar") || strings.HasSuffix(l, ".tgz") || strings.HasSuffix(l, ".tbz2") || strings.HasSuffix(l, ".txz")
}

// safeTarget is where an entry goes, refusing names like "../../x" that would escape the folder.
func safeTarget(dir, name string) (string, bool) {
	clean := strings.TrimLeft(strings.ReplaceAll(name, "\\", "/"), "/")
	if clean == "" {
		return "", false
	}
	p := filepath.Join(dir, filepath.FromSlash(clean))
	rel, err := filepath.Rel(dir, p)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", false
	}
	return p, true
}

func writeEntry(dir, name string, isDir bool, r io.Reader) error {
	p, ok := safeTarget(dir, name)
	if !ok {
		return nil
	}
	if isDir {
		return os.MkdirAll(p, 0o755)
	}
	os.MkdirAll(filepath.Dir(p), 0o755)
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	if r != nil {
		_, err = io.Copy(f, r)
	}
	return err
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

// unpack returns the folder, or the one file when the archive holds just one.
func (j *cvJob) unpack(in, dir string) (string, error) {
	l := strings.ToLower(filepath.Base(in))
	total := float64(max(1, fileSize(in)))
	switch {
	case strings.HasSuffix(l, ".rar"):
		return "", cvError("RAR files can't be unpacked by AnySave yet. ZIP, 7z and TAR work.")
	case strings.HasSuffix(l, ".zip") || strings.HasSuffix(l, ".cbz"):
		z, err := zip.OpenReader(in)
		if err != nil {
			return "", cvError("Couldn't open this ZIP. It may be damaged or incomplete.")
		}
		defer z.Close()
		for i, f := range z.File {
			if err := j.check(); err != nil {
				return "", err
			}
			if f.Flags&1 != 0 {
				return "", cvError("This ZIP is locked with a password. AnySave can't open locked ZIPs yet.")
			}
			if f.FileInfo().IsDir() {
				writeEntry(dir, f.Name, true, nil)
				continue
			}
			if f.Mode()&os.ModeSymlink != 0 {
				continue
			}
			r, err := f.Open()
			if err != nil {
				return "", cvError("This ZIP uses a kind of packing AnySave can't read.")
			}
			err = writeEntry(dir, f.Name, false, r)
			r.Close()
			if err != nil {
				return "", cvError("Couldn't unpack " + f.Name + ": " + err.Error())
			}
			j.progress(float64(i+1) * 100 / float64(len(z.File)))
		}
	case strings.HasSuffix(l, ".7z"):
		pw := j.spec.Opts["password"]
		var z *sevenzip.ReadCloser
		var err error
		if pw != "" {
			z, err = sevenzip.OpenReaderWithPassword(in, pw)
		} else {
			z, err = sevenzip.OpenReader(in)
		}
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "password") {
				return "", cvError("This 7z is locked. Type its password and try again.")
			}
			return "", cvError("Couldn't read this 7z file.")
		}
		defer z.Close()
		var done int64
		for _, f := range z.File {
			if err := j.check(); err != nil {
				return "", err
			}
			if f.FileInfo().IsDir() {
				writeEntry(dir, f.Name, true, nil)
				continue
			}
			r, err := f.Open()
			if err == nil {
				err = writeEntry(dir, f.Name, false, r)
				r.Close()
			}
			if err != nil {
				if pw != "" || strings.Contains(strings.ToLower(err.Error()), "password") {
					return "", cvError("Wrong password, or this 7z is damaged.")
				}
				return "", cvError("Couldn't read this 7z file. If it's locked, type its password.")
			}
			done += int64(f.UncompressedSize)
			j.progress(min(99, float64(done)*100/(total*3)))
		}
	default:
		fh, err := os.Open(in)
		if err != nil {
			return "", cvError("Couldn't open the archive.")
		}
		defer fh.Close()
		cr := &countingReader{r: bufio.NewReader(fh)}
		var s io.Reader = cr
		switch {
		case strings.HasSuffix(l, ".gz") || strings.HasSuffix(l, ".tgz"):
			g, err := gzip.NewReader(cr)
			if err != nil {
				return "", cvError("This GZ file is damaged.")
			}
			s = g
		case strings.HasSuffix(l, ".bz2") || strings.HasSuffix(l, ".tbz2"):
			s = bzip2.NewReader(cr)
		case strings.HasSuffix(l, ".xz") || strings.HasSuffix(l, ".txz"):
			x, err := xz.NewReader(cr)
			if err != nil {
				return "", cvError("This XZ file is damaged.")
			}
			s = x
		}
		if !isTarName(l) { // one compressed file, e.g. log.txt.gz
			name := archiveBase(filepath.Base(in))
			if name == "" {
				name = "file"
			}
			if err := writeEntry(dir, name, false, s); err != nil {
				return "", cvError("Couldn't unpack: the file is damaged.")
			}
		} else {
			t := tar.NewReader(s)
			for {
				if err := j.check(); err != nil {
					return "", err
				}
				h, err := t.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return "", cvError("Couldn't unpack: the archive is damaged or incomplete.")
				}
				switch h.Typeflag {
				case tar.TypeDir:
					writeEntry(dir, h.Name, true, nil)
				case tar.TypeReg, tar.TypeRegA:
					if err := writeEntry(dir, h.Name, false, t); err != nil {
						return "", cvError("Couldn't unpack " + h.Name + ".")
					}
				}
				j.progress(min(99, float64(cr.n)*100/total))
			}
		}
	}
	var files []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		os.RemoveAll(dir)
		return "", cvError("This archive is empty.")
	}
	j.progress(100)
	if len(files) == 1 { // a single file comes out as itself, not in a folder
		dst := uniquePath(filepath.Dir(dir), baseNoExt(files[0]), strings.TrimPrefix(filepath.Ext(files[0]), "."))
		if filepath.Ext(files[0]) == "" {
			dst = strings.TrimSuffix(dst, ".")
		}
		if os.Rename(files[0], dst) == nil {
			os.RemoveAll(dir)
			return dst, nil
		}
	}
	return dir, nil
}

type archiveEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// archiveList says what's inside, for the preview (at most max files).
func archiveList(in string, maxN int) ([]archiveEntry, bool, error) {
	l := strings.ToLower(filepath.Base(in))
	var out []archiveEntry
	locked := false
	switch {
	case strings.HasSuffix(l, ".rar"):
		return nil, false, cvError("RAR files can't be opened yet.")
	case strings.HasSuffix(l, ".zip") || strings.HasSuffix(l, ".cbz"):
		z, err := zip.OpenReader(in)
		if err != nil {
			return nil, false, cvError("Couldn't open this ZIP.")
		}
		defer z.Close()
		for _, f := range z.File {
			if f.Flags&1 != 0 {
				locked = true
			}
			if !f.FileInfo().IsDir() && len(out) < maxN {
				out = append(out, archiveEntry{f.Name, int64(f.UncompressedSize64)})
			}
		}
	case strings.HasSuffix(l, ".7z"):
		z, err := sevenzip.OpenReader(in)
		if err != nil {
			return nil, true, nil // most likely locked (names hidden too)
		}
		defer z.Close()
		for _, f := range z.File {
			if !f.FileInfo().IsDir() && len(out) < maxN {
				out = append(out, archiveEntry{f.Name, int64(f.UncompressedSize)})
			}
		}
	default:
		fh, err := os.Open(in)
		if err != nil {
			return nil, false, err
		}
		defer fh.Close()
		var s io.Reader = bufio.NewReader(fh)
		switch {
		case strings.HasSuffix(l, ".gz") || strings.HasSuffix(l, ".tgz"):
			g, err := gzip.NewReader(s)
			if err != nil {
				return nil, false, cvError("This GZ file is damaged.")
			}
			s = g
		case strings.HasSuffix(l, ".bz2") || strings.HasSuffix(l, ".tbz2"):
			s = bzip2.NewReader(s)
		case strings.HasSuffix(l, ".xz") || strings.HasSuffix(l, ".txz"):
			x, err := xz.NewReader(s)
			if err != nil {
				return nil, false, cvError("This XZ file is damaged.")
			}
			s = x
		}
		if !isTarName(l) {
			return []archiveEntry{{archiveBase(filepath.Base(in)), -1}}, false, nil
		}
		t := tar.NewReader(s)
		for len(out) < maxN {
			h, err := t.Next()
			if err != nil {
				break
			}
			if h.Typeflag == tar.TypeReg || h.Typeflag == tar.TypeRegA {
				out = append(out, archiveEntry{h.Name, h.Size})
			}
		}
	}
	return out, locked, nil
}

// pack puts the files in one ZIP or TAR.GZ.
func (j *cvJob) pack(paths []string, out string) error {
	var total, done int64
	for _, p := range paths {
		total += fileSize(p)
	}
	total = max(total, 1)
	names := map[string]bool{}
	nameOf := func(p string) string {
		n := filepath.Base(p)
		ext := filepath.Ext(n)
		for i := 1; names[strings.ToLower(n)]; i++ {
			n = strings.TrimSuffix(filepath.Base(p), ext) + " (" + itoa(i) + ")" + ext
		}
		names[strings.ToLower(n)] = true
		return n
	}
	f, err := os.Create(out)
	if err != nil {
		return cvError("Couldn't create the archive: " + err.Error())
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	copyIn := func(w io.Writer, p string) error {
		src, err := os.Open(p)
		if err != nil {
			return cvError("Couldn't read " + filepath.Base(p) + ".")
		}
		defer src.Close()
		buf := make([]byte, 1<<20)
		for {
			if err := j.check(); err != nil {
				return err
			}
			n, rerr := src.Read(buf)
			if n > 0 {
				if _, err := w.Write(buf[:n]); err != nil {
					return cvError("Couldn't write the archive: " + err.Error())
				}
				done += int64(n)
				j.progress(min(99, float64(done)*100/float64(total)))
			}
			if rerr == io.EOF {
				return nil
			}
			if rerr != nil {
				return cvError("Couldn't read " + filepath.Base(p) + ".")
			}
		}
	}
	if strings.HasSuffix(strings.ToLower(out), ".tar.gz") {
		gz := gzip.NewWriter(bw)
		t := tar.NewWriter(gz)
		for _, p := range paths {
			st, err := os.Stat(p)
			if err != nil {
				return cvError("Couldn't read " + filepath.Base(p) + ".")
			}
			h, _ := tar.FileInfoHeader(st, "")
			h.Name = nameOf(p)
			h.Format = tar.FormatPAX
			if err := t.WriteHeader(h); err != nil {
				return cvError("Couldn't write the archive: " + err.Error())
			}
			if err := copyIn(t, p); err != nil {
				return err
			}
		}
		if err := t.Close(); err != nil {
			return err
		}
		if err := gz.Close(); err != nil {
			return err
		}
	} else {
		z := zip.NewWriter(bw)
		for _, p := range paths {
			st, err := os.Stat(p)
			if err != nil {
				return cvError("Couldn't read " + filepath.Base(p) + ".")
			}
			h, _ := zip.FileInfoHeader(st)
			h.Name = nameOf(p)
			h.Method = zip.Deflate
			if cvVideoExt[extOf(p)] || cvAudioExt[extOf(p)] || setOf("jpg", "jpeg", "png", "webp", "heic", "avif", "zip", "7z", "gz", "xz", "bz2", "rar", "pdf")[extOf(p)] {
				h.Method = zip.Store // already packed tight: storing is much faster and as small
			}
			w, err := z.CreateHeader(h)
			if err != nil {
				return cvError("Couldn't write the archive: " + err.Error())
			}
			if err := copyIn(w, p); err != nil {
				return err
			}
		}
		if err := z.Close(); err != nil {
			return err
		}
	}
	if err := bw.Flush(); err != nil {
		return cvError("Couldn't write the archive: " + err.Error())
	}
	j.progress(100)
	return nil
}

func itoa(i int) string { return strconv.Itoa(i) }
