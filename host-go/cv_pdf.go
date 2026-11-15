package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// PDF tools with pdfcpu (Apache-2.0). Pages to pictures and PDF text are done in the page with pdf.js.

func pdfConf(password string) *model.Configuration {
	c := model.NewDefaultConfiguration()
	c.ValidationMode = model.ValidationRelaxed
	if password != "" {
		c.UserPW = password
		c.OwnerPW = password
	}
	return c
}

// pdfErr turns a pdfcpu error into a plain sentence.
func pdfErr(err error, name string, password string) error {
	if err == nil {
		return nil
	}
	if _, ok := err.(cvError); ok {
		return err
	}
	l := strings.ToLower(err.Error())
	logf("pdf: %s: %v", name, err)
	switch {
	case strings.Contains(l, "password") || strings.Contains(l, "encrypt"):
		if password == "" {
			return cvError(name + " is locked with a password. Use \"Remove a password\" first.")
		}
		return cvError("That password is wrong.")
	case strings.Contains(l, "no space"):
		return cvError("The disk is full. Free some space and try again.")
	}
	return cvError("Couldn't read " + name + ". It may be damaged or not really a PDF.")
}

func pdfPageCount(path string) (int, error) {
	return api.PageCountFile(context.Background(), path)
}

// pdfLocked says whether a PDF needs a password to open.
func pdfLocked(path string) bool {
	_, err := api.ReadContextFile(context.Background(), path)
	if err == nil {
		return false
	}
	l := strings.ToLower(err.Error())
	return strings.Contains(l, "password") || strings.Contains(l, "encrypt")
}

func (j *cvJob) pdfMerge(paths []string, out string) error {
	for _, p := range paths {
		if pdfLocked(p) {
			return cvError(filepath.Base(p) + " is locked with a password. Use \"Remove a password\" first.")
		}
	}
	j.progress(10)
	if err := api.MergeCreateFile(j.ctx, paths, out, false, pdfConf("")); err != nil {
		return pdfErr(err, "one of the PDFs", "")
	}
	j.progress(100)
	return nil
}

var rangeRe = regexp.MustCompile(`^(\d*)\s*[-–]\s*(\d*)$`)

// parseRanges turns "1-3, 5, 8-" into pages 1,2,3,5,8..last (1-based), in the order written.
func parseRanges(text string, count int) ([]int, error) {
	var pages []int
	seen := map[int]bool{}
	add := func(p int) {
		if p >= 1 && p <= count && !seen[p] {
			seen[p] = true
			pages = append(pages, p)
		}
	}
	for _, part := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		if m := rangeRe.FindStringSubmatch(part); m != nil {
			a, b := 1, count
			if m[1] != "" {
				a, _ = strconv.Atoi(m[1])
			}
			if m[2] != "" {
				b, _ = strconv.Atoi(m[2])
			}
			for p := a; p <= b; p++ {
				add(p)
			}
		} else if n, err := strconv.Atoi(part); err == nil {
			add(n)
		} else {
			return nil, cvError(fmt.Sprintf("%q isn't a page number. Write pages like 1-3, 5, 8-", part))
		}
	}
	if len(pages) == 0 {
		return nil, cvError(fmt.Sprintf("Those pages aren't in this PDF (it has %d pages).", count))
	}
	return pages, nil
}

func (j *cvJob) wantedPages(in string) ([]int, int, error) {
	if pdfLocked(in) {
		return nil, 0, cvError(filepath.Base(in) + " is locked with a password. Use \"Remove a password\" first.")
	}
	n, err := pdfPageCount(in)
	if err != nil {
		return nil, 0, pdfErr(err, filepath.Base(in), "")
	}
	if t := j.spec.opt("pages"); t != "" {
		p, err := parseRanges(t, n)
		return p, n, err
	}
	all := make([]int, n)
	for i := range all {
		all[i] = i + 1
	}
	return all, n, nil
}

func pageStrings(p []int) []string {
	s := make([]string, len(p))
	for i, n := range p {
		s[i] = strconv.Itoa(n)
	}
	return s
}

// pdfPick keeps the chosen pages, in the order written.
func (j *cvJob) pdfPick(in, out string) error {
	pages, _, err := j.wantedPages(in)
	if err != nil {
		return err
	}
	j.progress(20)
	if err := api.CollectFile(j.ctx, in, out, pageStrings(pages), pdfConf("")); err != nil {
		return pdfErr(err, filepath.Base(in), "")
	}
	j.progress(100)
	return nil
}

// pdfSplit saves every chosen page as its own PDF.
func (j *cvJob) pdfSplit(in, dir string) error {
	pages, n, err := j.wantedPages(in)
	if err != nil {
		return err
	}
	digits := len(strconv.Itoa(n))
	if digits < 2 {
		digits = 2
	}
	base := baseNoExt(in)
	for i, p := range pages {
		if err := j.check(); err != nil {
			return err
		}
		out := filepath.Join(dir, fmt.Sprintf("%s page %0*d.pdf", base, digits, p))
		if err := api.CollectFile(j.ctx, in, out, []string{strconv.Itoa(p)}, pdfConf("")); err != nil {
			return pdfErr(err, filepath.Base(in), "")
		}
		j.progress(float64(i+1) * 100 / float64(len(pages)))
	}
	return nil
}

func (j *cvJob) pdfRotate(in, out string) error {
	deg := j.spec.optI("angle", 90)
	deg = ((deg % 360) + 360) % 360
	if deg%90 != 0 || deg == 0 {
		return cvError("Pick a turn of 90, 180 or 270 degrees.")
	}
	var sel []string
	if t := j.spec.opt("pages"); t != "" {
		pages, _, err := j.wantedPages(in)
		if err != nil {
			return err
		}
		sel = pageStrings(pages)
	}
	if err := api.RotateFile(j.ctx, in, out, deg, sel, pdfConf("")); err != nil {
		return pdfErr(err, filepath.Base(in), "")
	}
	j.progress(100)
	return nil
}

// pdfProtect locks the PDF with AES-256: nobody can open it without the password.
func (j *cvJob) pdfProtect(in, out string) error {
	pw := j.spec.Opts["password"]
	if pw == "" {
		return cvError("Type a password first.")
	}
	if pdfLocked(in) {
		return cvError(filepath.Base(in) + " already has a password. Remove it first if you want a new one.")
	}
	conf := model.NewAESConfiguration(pw, pw, 256)
	conf.ValidationMode = model.ValidationRelaxed
	if err := api.EncryptFile(j.ctx, in, out, conf); err != nil {
		return pdfErr(err, filepath.Base(in), "")
	}
	j.progress(100)
	return nil
}

func (j *cvJob) pdfUnlock(in, out string) error {
	pw := j.spec.Opts["password"]
	if !pdfLocked(in) {
		// Some PDFs only limit printing or copying; saving them again removes that.
		if err := api.DecryptFile(j.ctx, in, out, pdfConf(pw)); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "not encrypted") {
				return cvError("This PDF has no password. There's nothing to remove.")
			}
			return pdfErr(err, filepath.Base(in), pw)
		}
		return nil
	}
	if pw == "" {
		return cvError("Type the PDF's password first.")
	}
	if err := api.DecryptFile(j.ctx, in, out, pdfConf(pw)); err != nil {
		return pdfErr(err, filepath.Base(in), pw)
	}
	j.progress(100)
	return nil
}

// pdfCompress re-saves every big picture inside as a smaller JPEG. Text and drawings stay as they are.
// opts.level: light, normal, strong.
func (j *cvJob) pdfCompress(in, out string) error {
	if pdfLocked(in) {
		return cvError(filepath.Base(in) + " is locked with a password. Use \"Remove a password\" first.")
	}
	maxSide, q := 1500, 65
	switch j.spec.opt("level") {
	case "strong":
		maxSide, q = 1000, 50
	case "light":
		maxSide, q = 2000, 80
	}
	fh, err := os.Open(in)
	if err != nil {
		return cvError("Couldn't read " + filepath.Base(in) + ".")
	}
	ctx, err := api.ReadValidateAndOptimize(j.ctx, fh, pdfConf(""), nil)
	fh.Close()
	if err != nil {
		return pdfErr(err, filepath.Base(in), "")
	}
	done := map[int]bool{}
	pages := ctx.PageCount
	for p := 1; p <= pages; p++ {
		if err := j.check(); err != nil {
			return err
		}
		imgs, err := pdfcpu.ExtractPageImages(j.ctx, ctx, p, false)
		if err != nil {
			logf("pdf compress: page %d: %v", p, err)
			continue
		}
		var nrs []int
		for nr := range imgs {
			nrs = append(nrs, nr)
		}
		sort.Ints(nrs)
		for _, nr := range nrs {
			im := imgs[nr]
			if done[im.ObjNr] || im.IsImgMask || im.HasImgMask || im.HasSMask || im.Thumb {
				continue
			}
			done[im.ObjNr] = true
			raw := new(bytes.Buffer)
			if im.Reader == nil {
				continue
			}
			if _, err := raw.ReadFrom(im); err != nil {
				continue
			}
			orig := int64(raw.Len())
			if im.Size > orig {
				orig = im.Size
			}
			if orig < 30<<10 { // small pictures aren't worth it
				continue
			}
			src, _, err := image.Decode(bytes.NewReader(raw.Bytes()))
			if err != nil {
				continue // a kind Go can't open (JPEG 2000, CMYK...): leave it
			}
			b := src.Bounds()
			w, h := b.Dx(), b.Dy()
			if big := max(w, h); big > maxSide {
				w, h = max(1, w*maxSide/big), max(1, h*maxSide/big)
				src = shrink(src, w, h)
			}
			var enc bytes.Buffer
			if err := jpeg.Encode(&enc, onWhite(src), &jpeg.Options{Quality: q}); err != nil {
				continue
			}
			if int64(enc.Len()) >= orig*9/10 {
				continue // not smaller: keep the original
			}
			// Swap the picture object for the smaller JPEG. Pages draw pictures scaled to their box,
			// so a picture with fewer pixels lands in exactly the same place.
			sd, _, _, err := model.CreateImageStreamDict(ctx.XRefTable, bytes.NewReader(enc.Bytes()))
			if err != nil {
				logf("pdf compress: replace %d: %v", im.ObjNr, err)
				continue
			}
			if entry, ok := ctx.FindTableEntry(im.ObjNr, 0); ok && entry != nil {
				entry.Object = *sd
			}
		}
		j.progress(float64(p) * 90 / float64(pages))
	}
	if err := api.WriteContextFile(j.ctx, ctx, out); err != nil {
		return pdfErr(err, filepath.Base(in), "")
	}
	// Also drop unused and repeated objects.
	tmp := out + ".opt.pdf"
	if err := api.OptimizeFile(j.ctx, out, tmp, pdfConf(""), nil); err == nil {
		if a, b := fileSize(tmp), fileSize(out); a > 0 && a < b {
			os.Rename(tmp, out)
		}
	}
	os.Remove(tmp)
	if fileSize(out) >= fileSize(in) {
		// Nothing to shrink (a text-only PDF, or already small): keep the original rather than a bigger copy.
		copyFile(in, out)
	}
	j.progress(100)
	return nil
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}
