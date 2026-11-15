package main

import (
	"bufio"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"strconv"
)

// Pictures: FFmpeg opens every kind (HEIC, AVIF, TIFF, WebP...) the right way up and turns it into PNG;
// JPEG and PNG are written here, WebP, AVIF, GIF, BMP, TIFF and ICO by FFmpeg. Photo info such as the
// location is never copied.

const maxDecodeSide = 8192 // so a 200-megapixel photo can't run out of memory

// decodePicture opens a picture no bigger than maxSide (0 = up to maxDecodeSide).
func (j *cvJob) decodePicture(in string, maxSide int) (image.Image, error) {
	if maxSide <= 0 || maxSide > maxDecodeSide {
		maxSide = maxDecodeSide
	}
	tmp := j.tmp(fmt.Sprintf("decoded-%d.png", randInt()))
	defer os.Remove(tmp)
	vf := fmt.Sprintf("scale=w='min(iw,%d)':h='min(ih,%d)':force_original_aspect_ratio=decrease", maxSide, maxSide)
	a := append([]string{"-i", in, "-frames:v", "1"}, pictureFilter(in, vf)...)
	err := j.quietFF(append(a, "-pix_fmt", "rgba", "-c:v", "png", "-f", "image2", "-update", "1", tmp))
	if err != nil {
		return nil, cvError("Couldn't open " + baseOf(in) + ". It may be damaged or a kind of picture AnySave can't read.")
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, cvError("Couldn't open " + baseOf(in) + ".")
	}
	defer f.Close()
	img, err := png.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, cvError("Couldn't open " + baseOf(in) + ".")
	}
	return img, nil
}

// pictureFilter applies vf to the picture. HEIC/AVIF photos made of tiles come out of FFmpeg's own
// tile-joining step, which only takes a "complex" filter.
func pictureFilter(in, vf string) []string {
	if setOf("heic", "heif", "hif", "avif")[extOf(in)] && probeMedia(in).TileW > 0 {
		return []string{"-filter_complex", "[0:g:0]" + vf}
	}
	return []string{"-vf", vf}
}

// quietFF runs FFmpeg without progress (quick steps).
func (j *cvJob) quietFF(args []string) error {
	c := *j
	c.progress = func(float64) {}
	return c.runFF(args, 0, "png")
}

func baseOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

var randState uint64 = 88172645463325252

func randInt() uint64 { // xorshift; only for temp names
	randState ^= randState << 13
	randState ^= randState >> 7
	randState ^= randState << 17
	return randState % 1_000_000_000
}

func hasAlpha(img image.Image) bool {
	switch m := img.(type) {
	case *image.NRGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 0xff {
				return true
			}
		}
		return false
	case *image.RGBA:
		for i := 3; i < len(m.Pix); i += 4 {
			if m.Pix[i] != 0xff {
				return true
			}
		}
		return false
	case *image.Gray, *image.YCbCr, *image.Gray16:
		return false
	}
	return true
}

// onWhite puts a picture with see-through parts on a white background (JPEG has no transparency).
func onWhite(img image.Image) image.Image {
	if !hasAlpha(img) {
		return img
	}
	b := img.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, &image.Uniform{color.White}, image.Point{}, draw.Src)
	draw.Draw(out, b, img, b.Min, draw.Over)
	return out
}

// shrink makes a picture smaller by averaging (good quality for downscaling).
func shrink(src image.Image, w, h int) image.Image {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if w >= sw || h >= sh || w < 1 || h < 1 {
		return src
	}
	in := image.NewNRGBA(b)
	draw.Draw(in, b, src, b.Min, draw.Src)
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	xr, yr := float64(sw)/float64(w), float64(sh)/float64(h)
	for y := 0; y < h; y++ {
		y0, y1 := int(float64(y)*yr), int(math.Min(float64(sh), math.Ceil(float64(y+1)*yr)))
		for x := 0; x < w; x++ {
			x0, x1 := int(float64(x)*xr), int(math.Min(float64(sw), math.Ceil(float64(x+1)*xr)))
			var r, g, bl, a, n uint64
			for yy := y0; yy < y1; yy++ {
				i := yy*in.Stride + x0*4
				for xx := x0; xx < x1; xx++ {
					r += uint64(in.Pix[i])
					g += uint64(in.Pix[i+1])
					bl += uint64(in.Pix[i+2])
					a += uint64(in.Pix[i+3])
					n++
					i += 4
				}
			}
			o := y*out.Stride + x*4
			out.Pix[o], out.Pix[o+1], out.Pix[o+2], out.Pix[o+3] = uint8(r/n), uint8(g/n), uint8(bl/n), uint8(a/n)
		}
	}
	return out
}

func jpegBytes(img image.Image, q int) []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	return buf.Bytes()
}

// image converts one picture to format, with opts quality (1–100) and targetKb, and the longest side
// limited to spec.Height when set.
func (j *cvJob) image(in, out, format string) error {
	maxSide := 0
	if j.spec.Height != nil {
		maxSide = *j.spec.Height
	}
	quality := j.spec.optI("quality", 85)
	if quality < 1 || quality > 100 {
		quality = 85
	}
	targetKb := j.spec.optI("targetKb", 0)
	img, err := j.decodePicture(in, maxSide)
	if err != nil {
		return err
	}
	data, err := j.encodePicture(img, format, quality, targetKb)
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return cvError("Couldn't save the picture: " + err.Error())
	}
	return nil
}

// encodePicture writes img as format. With targetKb, it finds the best quality (and if needed a
// smaller size) that fits.
func (j *cvJob) encodePicture(img image.Image, format string, quality, targetKb int) ([]byte, error) {
	switch format {
	case "jpg", "jpeg", "webp":
		enc := func(m image.Image, q int) ([]byte, error) {
			if format == "webp" {
				return j.ffEncode(m, "webp", q)
			}
			return jpegBytes(onWhite(m), q), nil
		}
		if targetKb <= 0 {
			return enc(img, quality)
		}
		limit := targetKb * 1024
		m := img
		for round := 0; round < 6; round++ {
			lo, hi := 15, min(quality, 95)
			var best []byte
			for lo <= hi {
				if err := j.check(); err != nil {
					return nil, err
				}
				q := (lo + hi) / 2
				b, err := enc(m, q)
				if err != nil {
					return nil, err
				}
				if len(b) <= limit {
					best, lo = b, q+1
				} else {
					hi = q - 1
				}
			}
			if best != nil {
				return best, nil
			}
			bb := m.Bounds()
			m = shrink(m, max(1, bb.Dx()*8/10), max(1, bb.Dy()*8/10))
		}
		return enc(m, 15)
	case "png":
		var buf bytes.Buffer
		e := png.Encoder{CompressionLevel: png.BestCompression}
		if err := e.Encode(&buf, img); err != nil {
			return nil, cvError("Couldn't make the PNG.")
		}
		return buf.Bytes(), nil
	case "bmp":
		return j.ffEncode(onWhite(img), format, quality)
	case "avif", "gif", "tiff", "tif", "ico":
		return j.ffEncode(img, format, quality)
	}
	return nil, cvError("Pictures can't be saved as " + format + ".")
}

// ffEncode hands a decoded picture to FFmpeg for the formats it writes better than Go.
func (j *cvJob) ffEncode(img image.Image, format string, quality int) ([]byte, error) {
	n := randInt()
	src := j.tmp(fmt.Sprintf("enc-%d.png", n))
	ext := format
	if ext == "tif" {
		ext = "tiff"
	}
	dst := j.tmp(fmt.Sprintf("enc-%d.%s", n, ext))
	defer os.Remove(src)
	defer os.Remove(dst)
	var buf bytes.Buffer
	(&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, img)
	if err := os.WriteFile(src, buf.Bytes(), 0o644); err != nil {
		return nil, cvError("Couldn't write a work file: " + err.Error())
	}
	a := []string{"-i", src}
	switch ext {
	case "webp":
		if quality >= 100 {
			a = append(a, "-c:v", "libwebp", "-lossless", "1")
		} else {
			a = append(a, "-c:v", "libwebp", "-quality", strconv.Itoa(quality), "-preset", "photo")
		}
	case "avif":
		crf := int(math.Max(10, math.Min(50, 63-float64(quality)*0.45)))
		a = append(a, "-c:v", "libaom-av1", "-still-picture", "1", "-crf", strconv.Itoa(crf), "-cpu-used", "6", "-pix_fmt", "yuv420p")
	case "ico":
		a = append(a, "-vf", "scale='min(256,iw)':'min(256,ih)':force_original_aspect_ratio=decrease")
	case "gif":
		a = append(a, "-vf", "split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse")
	case "bmp":
		a = append(a, "-pix_fmt", "bgr24")
	}
	a = append(a, "-frames:v", "1", "-update", "1", dst)
	if err := j.quietFF(a); err != nil {
		return nil, cvError("Couldn't make a " + format + " picture.")
	}
	b, err := os.ReadFile(dst)
	if err != nil || len(b) == 0 {
		return nil, cvError("Couldn't make a " + format + " picture.")
	}
	return b, nil
}

// imagesToPdf puts one picture on each page, in the order chosen. Opts: page (a4, letter, fit),
// margin (1 = white border), quality.
func (j *cvJob) imagesToPdf(paths []string, out string) error {
	q := j.spec.optI("quality", 80)
	maxSide := 2480 // A4 at about 210 dpi
	if q >= 90 {
		maxSide = 3508 // 300 dpi
	}
	margin := 0.0
	if j.spec.opt("margin") == "1" {
		margin = 28
	}
	var pages []pdfImagePage
	for i, p := range paths {
		if err := j.check(); err != nil {
			return err
		}
		img, err := j.decodePicture(p, maxSide)
		if err != nil {
			return err
		}
		b := img.Bounds()
		pages = append(pages, pdfImagePage{jpeg: jpegBytes(onWhite(img), q), w: b.Dx(), h: b.Dy()})
		j.progress(float64(i+1) * 95 / float64(len(paths)))
	}
	data := buildImagePdf(pages, j.spec.opt("page"), margin)
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return cvError("Couldn't save the PDF: " + err.Error())
	}
	j.progress(100)
	return nil
}

type pdfImagePage struct {
	jpeg []byte
	w, h int
}

// buildImagePdf writes a small, valid PDF with one JPEG per page, centred and fitted.
func buildImagePdf(pages []pdfImagePage, size string, margin float64) []byte {
	var buf bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) {
		offsets = append(offsets, buf.Len())
		fmt.Fprintf(&buf, "%d 0 obj\n%s", len(offsets), body)
		if stream != nil {
			buf.WriteString("\nstream\n")
			buf.Write(stream)
			buf.WriteString("\nendstream")
		}
		buf.WriteString("\nendobj\n")
	}
	buf.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(pages)
	kids := ""
	for i := 0; i < n; i++ {
		kids += fmt.Sprintf("%d 0 R ", 3+i*3)
	}
	obj("<< /Type /Catalog /Pages 2 0 R >>", nil)
	obj(fmt.Sprintf("<< /Type /Pages /Count %d /Kids [%s] >>", n, kids), nil)
	for i, p := range pages {
		landscape := p.w > p.h
		var pw, ph float64
		switch size {
		case "letter":
			pw, ph = 612, 792
		case "fit":
			pw, ph = float64(p.w)*72/150+2*margin, float64(p.h)*72/150+2*margin
		default:
			pw, ph = 595.28, 841.89
		}
		if size != "fit" && landscape {
			pw, ph = ph, pw
		}
		aw, ah := pw-2*margin, ph-2*margin
		s := math.Min(aw/float64(p.w), ah/float64(p.h))
		w, h := float64(p.w)*s, float64(p.h)*s
		x, y := margin+(aw-w)/2, margin+(ah-h)/2
		content := fmt.Sprintf("q %.2f 0 0 %.2f %.2f %.2f cm /Im0 Do Q", w, h, x, y)
		base := 3 + i*3
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %.2f %.2f] /Resources << /XObject << /Im0 %d 0 R >> >> /Contents %d 0 R >>",
			pw, ph, base+2, base+1), nil)
		obj(fmt.Sprintf("<< /Length %d >>", len(content)), []byte(content))
		obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>",
			p.w, p.h, len(p.jpeg)), p.jpeg)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, xref)
	return buf.Bytes()
}
