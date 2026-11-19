package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"anysave-host/arc"
)

// Archives: the jobs behind the Archives page (and the Convert page's Unpack / Pack). The archive
// engine itself is the arc package: ZIP, 7z, RAR, TAR (.gz .bz2 .xz .zst), single compressed files
// and ISO images in; ZIP and 7z out. RAR is never made (RARLAB's licence doesn't allow it).

const (
	cvArcTest    = "arc-test"
	cvArcRepair  = "arc-repair"
	cvArcConvert = "arc-convert"
	cvArcEdit    = "arc-edit"
	cvArcSplit   = "arc-split"
	cvArcJoin    = "arc-join"
)

var doubleExt = []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst"}

// archiveBase is the name without the archive extension(s) or part numbers.
func archiveBase(name string) string {
	name = arc.SetName(name)
	l := strings.ToLower(name)
	for _, e := range doubleExt {
		if strings.HasSuffix(l, e) {
			return name[:len(name)-len(e)]
		}
	}
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// arcErr turns an engine error into plain words.
func arcErr(err error) error {
	if err == nil {
		return nil
	}
	var d *arc.DamagedError
	var u *arc.UnsupportedError
	var b *arc.BombError
	var pe *fs.PathError
	switch {
	case errors.Is(err, arc.ErrCancelled):
		return errCancelled
	case errors.Is(err, arc.ErrNeedPassword):
		return cvError("This archive is locked. Type its password and try again.")
	case errors.Is(err, arc.ErrWrongPassword):
		return cvError("Wrong password. Check it (capital letters count) and try again.")
	case errors.As(err, &b):
		return cvError(b.Detail)
	case errors.As(err, &u):
		return cvError(u.Detail)
	case errors.As(err, &d):
		return cvError("The archive is damaged (" + d.Detail + "). Try \"Repair\" for a ZIP, or \"Recover files\" to save what can be read.")
	case errors.Is(err, fs.ErrPermission):
		return cvError("AnySave isn't allowed to write there. Choose another folder.")
	case errors.As(err, &pe):
		if errors.Is(pe.Err, fs.ErrNotExist) {
			return cvError(filepath.Base(pe.Path) + " isn't there any more.")
		}
		return cvError("Couldn't use " + filepath.Base(pe.Path) + ": " + pe.Err.Error())
	}
	var ce cvError
	if errors.As(err, &ce) {
		return err
	}
	return cvError(err.Error())
}

// isPreviewCopy is true for a file dropped on the page (a copy in AnySave's temporary folder).
func isPreviewCopy(p string) bool {
	return strings.Contains(p, string(filepath.Separator)+"anysave-preview-")
}

// arcDest is where results go: "next" to the archive (the default), "downloads", or a folder the
// user chose. A dropped file is only a temporary copy, so its results go to Downloads.
func (j *cvJob) arcDest(archive, outDir string) string {
	d := j.spec.opt("dest")
	switch {
	case d == "" && outDir != "":
		return outDir
	case d == "downloads" || ((d == "next" || d == "") && isPreviewCopy(archive)):
		return filepath.Join(downloadDir, "Unpacked")
	case d == "next" || d == "":
		return filepath.Dir(archive)
	}
	return d
}

func (j *cvJob) arcProgress(stage string) func(done, total int64, name string) {
	j.stage(stage)
	return func(done, total int64, _ string) {
		if total > 0 {
			j.progress(min(99, float64(done)*100/float64(total)))
		}
	}
}

func (j *cvJob) note(s string) {
	if j.noteFn != nil {
		j.noteFn(s)
	}
}

func jsonList(s string) []string {
	var out []string
	if s != "" {
		json.Unmarshal([]byte(s), &out)
	}
	return out
}

func plural(n int, one string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + one + "s"
}

// ---------- unpack ----------

func (j *cvJob) unpackArc(in, outDir string) (string, error) {
	a, err := arc.Open(in, arc.Options{Password: j.spec.opt("password")})
	if err != nil {
		return "", arcErr(err)
	}
	defer a.Close()
	dest := j.arcDest(in, outDir)
	name := j.spec.opt("name")
	if name == "" {
		name = archiveBase(filepath.Base(arc.FirstPart(in)))
	}
	res, err := arc.Extract(j.ctx, a, arc.ExtractOptions{
		Dest: dest, Name: safeName(name), Mode: j.spec.opt("mode"), Policy: j.spec.opt("policy"),
		Only: jsonList(j.spec.opt("only")), Base: j.spec.opt("base"),
		KeepJunk: j.spec.opt("keepJunk") == "1", KeepGoing: j.spec.opt("recover") == "1",
		Progress: j.arcProgress("Unpacking"), FreeSpace: freeSpace,
	})
	if err != nil {
		return "", arcErr(err)
	}
	parts := []string{"Unpacked " + plural(res.Files, "file") + " (" + arc.Bytes(res.Bytes) + ")"}
	if n := len(res.Skipped); n > 0 {
		parts = append(parts, "skipped "+plural(n, "unsafe item")+" ("+res.Skipped[0].Why+")")
	}
	if n := len(res.Damaged); n > 0 {
		parts = append(parts, plural(n, "damaged file")+" left out")
	}
	if res.Existing > 0 {
		switch j.spec.opt("policy") {
		case "skip":
			parts = append(parts, plural(res.Existing, "name")+" already there kept as they were")
		case "replace":
			parts = append(parts, plural(res.Existing, "item")+" replaced")
		default:
			parts = append(parts, plural(res.Existing, "name")+" already taken, so kept both")
		}
	}
	j.note(strings.Join(parts, " · ") + ".")
	j.progress(100)
	return res.Path, nil
}

// ---------- pack ----------

func (j *cvJob) packArc(paths []string, out string) (string, error) {
	o := arc.CreateOptions{
		Format: j.spec.Format, Level: j.spec.optI("level", 5), Password: j.spec.opt("password"),
		EncryptNames: j.spec.opt("encryptNames") == "1", Solid: j.spec.opt("solid") == "1",
		Comment: j.spec.Opts["comment"], KeepJunk: j.spec.opt("keepJunk") == "1",
	}
	if mb := j.spec.optF("splitMb", 0); mb > 0 {
		o.Split = int64(mb * 1e6)
		if mb == 4096 { // "4 GB for FAT32 USB sticks": just under FAT32's 4 GiB file limit
			o.Split = 4<<30 - 1
		}
	}
	res, err := arc.Create(j.ctx, out, paths, o, j.arcProgress("Packing"))
	if err != nil {
		return "", arcErr(err)
	}
	what := plural(res.Files, "file")
	sz := int64(0)
	for _, p := range res.Parts {
		sz += fileSize(p)
	}
	n := what + " packed into " + arc.Bytes(sz)
	if len(res.Parts) > 1 {
		n += " in " + strconv.Itoa(len(res.Parts)) + " parts"
	}
	if o.Password != "" {
		n += ", locked with AES-256"
		if o.EncryptNames {
			n += " (names hidden too)"
		}
	}
	j.note(n + ".")
	j.progress(100)
	return res.Parts[0], nil // the first part when it was split
}

// packTarGz puts files in one TAR.GZ (Convert page only).
func (j *cvJob) packTarGz(paths []string, out string) error {
	var total, done int64
	for _, p := range paths {
		total += fileSize(p)
	}
	total = max(total, 1)
	f, err := os.Create(out)
	if err != nil {
		return cvError("Couldn't create the archive: " + err.Error())
	}
	defer f.Close()
	bw := bufio.NewWriterSize(f, 1<<20)
	gz := gzip.NewWriter(bw)
	t := tar.NewWriter(gz)
	names := map[string]bool{}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			return cvError("Couldn't read " + filepath.Base(p) + ".")
		}
		n := filepath.Base(p)
		for i := 2; names[strings.ToLower(n)]; i++ {
			n = strings.TrimSuffix(filepath.Base(p), filepath.Ext(p)) + " (" + strconv.Itoa(i) + ")" + filepath.Ext(p)
		}
		names[strings.ToLower(n)] = true
		h, _ := tar.FileInfoHeader(st, "")
		h.Name = n
		h.Format = tar.FormatPAX
		if err := t.WriteHeader(h); err != nil {
			return cvError("Couldn't write the archive: " + err.Error())
		}
		src, err := os.Open(p)
		if err != nil {
			return cvError("Couldn't read " + filepath.Base(p) + ".")
		}
		pr := &arcCount{r: src, fn: func(n int64) { done += n; j.progress(min(99, float64(done)*100/float64(total))) }, j: j}
		_, err = io.Copy(t, pr)
		src.Close()
		if err != nil {
			return err
		}
	}
	if err := t.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return cvError("Couldn't write the archive: " + err.Error())
	}
	j.progress(100)
	return nil
}

type arcCount struct {
	r  io.Reader
	fn func(int64)
	j  *cvJob
}

func (c *arcCount) Read(p []byte) (int, error) {
	if err := c.j.check(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(p)
	c.fn(int64(n))
	return n, err
}

// ---------- test, repair, convert, edit, split, join ----------

func (j *cvJob) testArc(in string) (string, error) {
	a, err := arc.Open(in, arc.Options{Password: j.spec.opt("password")})
	if err != nil {
		return "", arcErr(err)
	}
	defer a.Close()
	res, err := arc.Test(j.ctx, a, j.arcProgress("Testing"))
	if err != nil {
		return "", arcErr(err)
	}
	if len(res.Bad) == 0 {
		if res.Files == 1 {
			j.note("The file is OK (" + arc.Bytes(res.Bytes) + " checked).")
		} else {
			j.note("All " + plural(res.Files, "file") + " are OK (" + arc.Bytes(res.Bytes) + " checked).")
		}
	} else {
		names := []string{}
		for i, b := range res.Bad {
			if i == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, b.Name)
		}
		j.note(strings.Replace(fmt.Sprintf("%s damaged: %s. %s OK.", plural(len(res.Bad), "file"), strings.Join(names, ", "), plural(res.Files, "other file")), "1 files", "1 file", 1))
	}
	j.progress(100)
	return in, nil
}

func (j *cvJob) repairArc(in, outDir string) (string, error) {
	parts, _ := arc.Parts(in)
	dest := j.arcDest(in, outDir)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return "", arcErr(err)
	}
	if arc.Kind(parts[0]) == "zip" || isZipFile(parts[0]) {
		out := arc.RepairOut(dest, arc.SetName(parts[0]))
		j.stage("Repairing")
		res, err := arc.RepairZip(j.ctx, parts, out, j.spec.opt("password"), func(d, t int64) {
			if t > 0 {
				j.progress(min(99, float64(d)*100/float64(t)))
			}
		})
		if err != nil {
			return "", arcErr(err)
		}
		n := "Saved " + plural(res.Kept, "item") + " in a new ZIP"
		if len(res.Dropped) > 0 {
			n += "; " + plural(len(res.Dropped), "damaged file") + " left out (" + res.Dropped[0] + ")"
		}
		if res.Locked > 0 {
			n += "; " + plural(res.Locked, "locked file") + " kept without checking (no password given)"
		}
		j.note(n + ".")
		return out, nil
	}
	// Other formats can't be rebuilt: save every file that can still be read.
	j.spec.Opts["recover"] = "1"
	j.spec.Opts["mode"] = "folder"
	if j.spec.opt("name") == "" {
		j.spec.Opts["name"] = archiveBase(filepath.Base(parts[0])) + " (recovered)"
	}
	j.spec.Opts["dest"] = dest
	return j.unpackArc(in, outDir)
}

func isZipFile(p string) bool {
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	b := make([]byte, 4)
	io.ReadFull(f, b)
	return string(b) == "PK\x03\x04"
}

func (j *cvJob) convertArc(in, outDir string) (string, error) {
	a, err := arc.Open(in, arc.Options{Password: j.spec.opt("password")})
	if err != nil {
		return "", arcErr(err)
	}
	defer a.Close()
	format := j.spec.Format
	if format != "7z" {
		format = "zip"
	}
	dest := j.arcDest(in, outDir)
	os.MkdirAll(dest, 0o755)
	out := arc.UniquePath(filepath.Join(dest, safeName(archiveBase(filepath.Base(arc.FirstPart(in))))+"."+format))
	o := arc.CreateOptions{Format: format, Level: j.spec.optI("level", 5), Password: j.spec.opt("newPassword"),
		EncryptNames: j.spec.opt("encryptNames") == "1", Solid: j.spec.opt("solid") == "1", Comment: a.Info().Comment}
	if mb := j.spec.optF("splitMb", 0); mb > 0 {
		o.Split = int64(mb * 1e6)
	}
	res, err := arc.Convert(j.ctx, a, out, o, j.arcProgress("Converting"))
	if err != nil {
		return "", arcErr(err)
	}
	kind := "ZIP"
	if format == "7z" {
		kind = "7z"
	}
	j.note(plural(res.Files, "file") + " moved into a new " + kind + ".")
	j.progress(100)
	return res.Parts[0], nil
}

func (j *cvJob) editArc(in string) (string, error) {
	o := arc.EditOptions{Password: j.spec.opt("password"), Delete: jsonList(j.spec.opt("delete")),
		Add: jsonList(j.spec.opt("add")), AddTo: j.spec.opt("addTo"), Level: j.spec.optI("level", 0)}
	if r := j.spec.opt("rename"); r != "" {
		json.Unmarshal([]byte(r), &o.Rename)
	}
	if j.spec.opt("setComment") == "1" {
		c := j.spec.Opts["comment"]
		o.Comment = &c
	}
	if err := arc.Edit(j.ctx, in, o, j.arcProgress("Updating the archive")); err != nil {
		return "", arcErr(err)
	}
	var did []string
	if n := len(o.Add); n > 0 {
		did = append(did, "added "+plural(n, "item"))
	}
	if n := len(o.Delete); n > 0 {
		did = append(did, "deleted "+plural(n, "item"))
	}
	if n := len(o.Rename); n > 0 {
		did = append(did, "renamed "+plural(n, "item"))
	}
	if o.Comment != nil {
		did = append(did, "saved the comment")
	}
	if len(did) > 0 {
		s := strings.Join(did, ", ")
		j.note(strings.ToUpper(s[:1]) + s[1:] + ".")
	}
	j.progress(100)
	return in, nil
}

func (j *cvJob) splitArc(in string) (string, error) {
	mb := j.spec.optF("splitMb", 0)
	if mb <= 0 {
		return "", cvError("Choose a part size.")
	}
	size := int64(mb * 1e6)
	if mb == 4096 {
		size = 4<<30 - 1
	}
	if fileSize(in) <= size {
		return "", cvError("The file is already smaller than one part.")
	}
	if isPreviewCopy(in) {
		return "", cvError("Choose the file with \"Choose a file\" (not drag and drop), so the parts can be saved next to it.")
	}
	j.stage("Splitting")
	parts, err := arc.SplitFile(j.ctx, in, size, func(d, t int64) { j.progress(min(99, float64(d)*100/float64(max(t, 1)))) })
	if err != nil {
		return "", arcErr(err)
	}
	j.note("Split into " + strconv.Itoa(len(parts)) + " parts (" + filepath.Base(parts[0]) + " …). Open the .001 part to use them again.")
	return parts[0], nil
}

func (j *cvJob) joinArc(in, outDir string) (string, error) {
	j.stage("Joining")
	dest := j.arcDest(in, outDir)
	os.MkdirAll(dest, 0o755)
	out, err := arc.JoinParts(j.ctx, in, dest, func(d, t int64) { j.progress(min(99, float64(d)*100/float64(max(t, 1)))) })
	if err != nil {
		return "", arcErr(err)
	}
	j.note("Joined into " + filepath.Base(out) + " (" + arc.Bytes(fileSize(out)) + ").")
	return out, nil
}

// ---------- the Convert page's preview ----------

type archiveEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// archiveList says what's inside, for the Convert page's preview (at most maxN files).
func archiveList(in string, maxN int) ([]archiveEntry, bool, error) {
	a, err := arc.Open(in, arc.Options{})
	if errors.Is(err, arc.ErrNeedPassword) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, arcErr(err)
	}
	defer a.Close()
	var out []archiveEntry
	for _, e := range a.Entries() {
		if !e.Dir && len(out) < maxN {
			out = append(out, archiveEntry{e.Name, e.Size})
		}
	}
	return out, a.Info().Encrypted, nil
}

func itoa(i int) string { return strconv.Itoa(i) }

func jsonString(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func init() { arc.HideWindow = hideWindow }
