package arc

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ISO images (CDs, DVDs, disk images) are read through bsdtar, which is part of macOS and of
// Windows 10 and later (as tar.exe). AnySave asks it for the list, and for the data as a TAR stream.

func isISO(src *multiFile) bool {
	for _, off := range []int64{0x8001, 0x8801, 0x9001} {
		b := make([]byte, 5)
		if _, err := src.ReadAt(b, off); err == nil && (string(b) == "CD001" || string(b) == "BEA01") {
			return true
		}
	}
	return false
}

// HideWindow is set by the app on Windows so bsdtar doesn't flash a console window.
var HideWindow = func(*exec.Cmd) {}

func bsdtarPath() string {
	switch runtime.GOOS {
	case "darwin":
		if isFile("/usr/bin/bsdtar") {
			return "/usr/bin/bsdtar"
		}
	case "windows":
		p := filepath.Join(os.Getenv("SystemRoot"), "System32", "tar.exe")
		if isFile(p) {
			return p
		}
	}
	if p, err := exec.LookPath("bsdtar"); err == nil {
		return p
	}
	return ""
}

type isoArchive struct {
	path    string
	info    Info
	entries []*Entry
}

var isoLine = regexp.MustCompile(`^(\S{10})\s+\d+\s+\S+\s+\S+\s+(\d+)\s+(\w{3}\s+\d+\s+[\d:]+)\s(.*)$`)

func openISO(src *multiFile, parts []string) (Archive, error) {
	info := baseInfo("iso", src, parts)
	src.Close()
	if len(parts) > 1 {
		return nil, &UnsupportedError{Detail: "Join the parts first, then open the ISO."}
	}
	bt := bsdtarPath()
	if bt == "" {
		return nil, &UnsupportedError{Detail: "Opening ISO images needs the system's tar tool, which isn't on this computer."}
	}
	cmd := exec.Command(bt, "-tvf", parts[0])
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	HideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil, damaged("this disk image can't be read")
	}
	a := &isoArchive{path: parts[0], info: info}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		m := isoLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		name := unescapeOctal(m[4])
		e := &Entry{Packed: -1}
		e.Size, _ = strconv.ParseInt(m[2], 10, 64)
		e.Modified = parseLsTime(m[3])
		switch m[1][0] {
		case 'd':
			e.Dir, e.Size = true, 0
		case 'l':
			if i := strings.Index(name, " -> "); i >= 0 {
				e.Link, name = name[i+4:], name[:i]
			} else {
				e.Link = "?"
			}
		case '-':
		default:
			e.Special = true
		}
		e.raw = name
		if c, _ := cleanName(name); c == "" {
			continue // the root "."
		}
		a.entries = append(a.entries, e)
	}
	finishEntries(a.entries)
	return a, nil
}

func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+4 <= len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b = append(b, byte(v))
				i += 3
				continue
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

func parseLsTime(s string) time.Time {
	s = strings.Join(strings.Fields(s), " ")
	if t, err := time.ParseInLocation("Jan 2 2006", s, time.Local); err == nil {
		return t
	}
	if t, err := time.ParseInLocation("Jan 2 15:04", s, time.Local); err == nil {
		now := time.Now()
		t = t.AddDate(now.Year(), 0, 0)
		if t.After(now.AddDate(0, 0, 1)) {
			t = t.AddDate(-1, 0, 0)
		}
		return t
	}
	return time.Time{}
}

func (a *isoArchive) Info() Info        { return a.info }
func (a *isoArchive) Entries() []*Entry { return a.entries }
func (a *isoArchive) Close() error      { return nil }

func (a *isoArchive) Walk(ctx context.Context, want func(*Entry) bool, fn func(*Entry, io.Reader) error) error {
	byName := map[string]*Entry{}
	left := 0
	for _, e := range a.entries {
		if want(e) {
			byName[e.Name] = e
			left++
		}
	}
	if left == 0 {
		return nil
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(cctx, bsdtarPath(), "-cf", "-", "--format", "pax", "@"+a.path)
	cmd.Env = append(os.Environ(), "LC_ALL=en_US.UTF-8")
	HideWindow(cmd)
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return &UnsupportedError{Detail: "The system's tar tool couldn't start."}
	}
	defer cmd.Wait()
	t := tar.NewReader(bufio.NewReaderSize(out, 1<<20))
	for left > 0 {
		if ctx.Err() != nil {
			return ErrCancelled
		}
		h, err := t.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return damaged("this disk image can't be read")
		}
		name, _ := cleanName(h.Name)
		e := byName[name]
		if e == nil {
			continue
		}
		delete(byName, name)
		left--
		var r io.Reader
		if !e.Dir && !e.Special && e.Link == "" && e.Unsafe == "" {
			r = ctxReader{ctx, &tarDataErr{t}}
		}
		if err := fn(e, r); err != nil {
			return stopOK(err)
		}
	}
	return nil
}
