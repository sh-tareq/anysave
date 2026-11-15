package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"hash/crc32"
	"html"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Documents: every input is first turned into simple HTML (headings, paragraphs, lists, bold, italic),
// then written as PDF (through the installed Chrome or Edge, so every language's letters come out
// right), text, a web page or an EPUB ebook. Pictures and complex layouts are left out.

func (j *cvJob) doc(in, out, format string) error {
	ext := extOf(in)
	if ext == format {
		return cvError("The file is already " + strings.ToUpper(ext) + ". Pick another format.")
	}
	title := baseNoExt(in)
	var body string
	var err error
	if ext == "pdf" {
		// The page reads the PDF's text with pdf.js and hands it over as a text file.
		tf := j.spec.opt("textFile")
		if tf == "" || !isFile(tf) {
			return cvError("Couldn't read the PDF's text. Reload the Convert page and try again.")
		}
		b, _ := os.ReadFile(tf)
		body = textToHTML(string(b))
	} else {
		body, err = docToHTML(in)
		if err != nil {
			return err
		}
	}
	j.progress(40)
	if strings.TrimSpace(htmlToText(body)) == "" {
		if ext == "pdf" {
			return cvError("No text found. This PDF may be scanned pictures of pages.")
		}
		return cvError("No text found in this file.")
	}
	if err := j.check(); err != nil {
		return err
	}
	switch format {
	case "pdf":
		err = j.htmlToPdf(title, body, out)
	case "txt":
		err = os.WriteFile(out, []byte(htmlToText(body)+"\n"), 0o644)
	case "html":
		err = os.WriteFile(out, []byte(htmlPage(title, body, "")), 0o644)
	case "epub":
		err = writeEpub(title, body, out)
	default:
		return cvError("Documents can't be saved as " + format + ".")
	}
	if err != nil {
		if _, ok := err.(cvError); ok {
			return err
		}
		return cvError("Couldn't save the document: " + err.Error())
	}
	j.progress(100)
	return nil
}

// ---------- readers ----------

// docToHTML reads any supported document as simple HTML (also used by the preview).
func docToHTML(in string) (string, error) {
	switch extOf(in) {
	case "docx":
		return zipDoc(in, docxHTML)
	case "odt":
		return zipDoc(in, odtHTML)
	case "epub":
		return zipDoc(in, epubHTML)
	case "rtf":
		if runtime.GOOS == "darwin" { // macOS reads RTF itself
			out, err := exec.Command("textutil", "-convert", "html", "-stdout", in).Output()
			if err == nil {
				return cleanHTML(string(out)), nil
			}
		}
		return "", cvError("RTF documents can only be read on a Mac for now.")
	}
	b, err := os.ReadFile(in)
	if err != nil {
		return "", cvError("Couldn't read " + filepath.Base(in) + ".")
	}
	s := strings.TrimPrefix(string(b), "\ufeff")
	switch extOf(in) {
	case "md", "markdown":
		return markdownHTML(s), nil
	case "html", "htm", "xhtml":
		return cleanHTML(s), nil
	}
	return textToHTML(s), nil
}

func zipDoc(in string, read func(*zip.Reader) (string, error)) (string, error) {
	z, err := zip.OpenReader(in)
	if err != nil {
		return "", cvError("Couldn't open " + filepath.Base(in) + ". It may be damaged.")
	}
	defer z.Close()
	return read(&z.Reader)
}

func zipRead(z *zip.Reader, name string) ([]byte, bool) {
	for _, f := range z.File {
		if f.Name == name {
			r, err := f.Open()
			if err != nil {
				return nil, false
			}
			defer r.Close()
			b, err := io.ReadAll(io.LimitReader(r, 200<<20))
			return b, err == nil
		}
	}
	return nil, false
}

func esc(s string) string { return html.EscapeString(s) }

var blankLinesRe = regexp.MustCompile(`\n\s*\n`)

func textToHTML(text string) string {
	var b strings.Builder
	for _, p := range blankLinesRe.Split(strings.ReplaceAll(text, "\r\n", "\n"), -1) {
		if p = strings.TrimSpace(p); p != "" {
			b.WriteString("<p>" + strings.ReplaceAll(esc(p), "\n", "<br>") + "</p>\n")
		}
	}
	return b.String()
}

var (
	bodyRe    = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	junkRe    = regexp.MustCompile(`(?is)<(script|style|svg|noscript|head|template)\b[^>]*>.*?</(script|style|svg|noscript|head|template)>`)
	imgRe     = regexp.MustCompile(`(?is)<img[^>]*>`)
	commentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
	eventRe   = regexp.MustCompile(`(?i)\s(on\w+|style|class|id)\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
)

// cleanHTML keeps the body's text structure and drops scripts, styles, pictures and event handlers.
func cleanHTML(s string) string {
	if m := bodyRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = commentRe.ReplaceAllString(s, "")
	s = junkRe.ReplaceAllString(s, "")
	s = imgRe.ReplaceAllString(s, "")
	s = eventRe.ReplaceAllString(s, "")
	return s
}

var (
	tagRe      = regexp.MustCompile(`(?s)<[^>]+>`)
	blockEndRe = regexp.MustCompile(`(?i)</(p|h[1-6]|li|div|blockquote|pre|tr|table|section|article)>|<br\s*/?>`)
	manyNLRe   = regexp.MustCompile(`\n{3,}`)
	spaceRunRe = regexp.MustCompile(`[ \t]+`)
)

// htmlToText gives readable plain text with blank lines between paragraphs.
func htmlToText(h string) string {
	h = regexp.MustCompile(`(?i)<li[^>]*>`).ReplaceAllString(h, "• ")
	h = blockEndRe.ReplaceAllStringFunc(h, func(m string) string {
		if strings.HasPrefix(strings.ToLower(m), "<br") {
			return "\n"
		}
		return "\n\n"
	})
	t := html.UnescapeString(tagRe.ReplaceAllString(h, ""))
	lines := strings.Split(t, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(spaceRunRe.ReplaceAllString(l, " "))
	}
	return strings.TrimSpace(manyNLRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

func attr(se xml.StartElement, name string) (string, bool) {
	for _, a := range se.Attr {
		n := a.Name.Local
		if a.Name.Space != "" {
			n = a.Name.Space + ":" + a.Name.Local
		}
		if a.Name.Local == name || n == name {
			return a.Value, true
		}
	}
	return "", false
}

func onOff(se xml.StartElement) bool {
	v, ok := attr(se, "val")
	return !ok || v == "1" || v == "true" || v == "on"
}

var headingStyleRe = regexp.MustCompile(`(?i)^heading\s*(\d)`)

// docxHTML reads Word .docx: paragraphs, headings (Heading 1–6, Title, or just big text), list items,
// bold/italic/underline.
func docxHTML(z *zip.Reader) (string, error) {
	data, ok := zipRead(z, "word/document.xml")
	if !ok {
		return "", cvError("This doesn't look like a Word document.")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	var out strings.Builder
	var para *strings.Builder
	style := ""
	list, bold, italic, under, inText := false, false, false, false, false
	size, maxSize := 0, 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "p":
				para = &strings.Builder{}
				style, list, maxSize = "", false, 0
			case "sz":
				v, _ := attr(t, "val")
				size, _ = strconv.Atoi(v)
			case "pStyle":
				style, _ = attr(t, "val")
			case "numPr":
				list = true
			case "r":
				bold, italic, under, size = false, false, false, 0
			case "b":
				bold = onOff(t)
			case "i":
				italic = onOff(t)
			case "u":
				v, ok := attr(t, "val")
				under = ok && v != "none"
			case "t":
				inText = true
			case "tab":
				if para != nil && inRun(d) {
					para.WriteString(" ")
				}
			case "br", "cr":
				if para != nil {
					para.WriteString("<br>")
				}
			}
		case xml.CharData:
			if inText && para != nil {
				s := esc(string(t))
				if under {
					s = "<u>" + s + "</u>"
				}
				if italic {
					s = "<i>" + s + "</i>"
				}
				if bold {
					s = "<b>" + s + "</b>"
				}
				para.WriteString(s)
				if strings.TrimSpace(string(t)) != "" && size > maxSize {
					maxSize = size
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "tc":
				if para != nil {
					para.WriteString(" ")
				}
			case "p":
				if para == nil {
					continue
				}
				text := para.String()
				para = nil
				if strings.TrimSpace(htmlToText(text)) == "" {
					continue
				}
				level := 0
				if m := headingStyleRe.FindStringSubmatch(style); m != nil {
					level, _ = strconv.Atoi(m[1])
				} else if strings.EqualFold(style, "Title") {
					level = 1
				} else if !list && len(text) < 300 {
					switch {
					case maxSize >= 36:
						level = 1
					case maxSize >= 30:
						level = 2
					case maxSize >= 27:
						level = 3
					}
				}
				switch {
				case level > 0:
					level = min(level, 6)
					fmt.Fprintf(&out, "<h%d>%s</h%d>\n", level, text, level)
				case list:
					out.WriteString("<p>• " + text + "</p>\n")
				default:
					out.WriteString("<p>" + text + "</p>\n")
				}
			}
		}
	}
	return out.String(), nil
}

func inRun(*xml.Decoder) bool { return true }

// odtHTML reads OpenDocument .odt: headings, paragraphs and list items.
func odtHTML(z *zip.Reader) (string, error) {
	data, ok := zipRead(z, "content.xml")
	if !ok {
		return "", cvError("This doesn't look like an ODT document.")
	}
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	type blk struct {
		tag string
		b   strings.Builder
	}
	var stack []*blk
	var out strings.Builder
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "h":
				lv := 2
				if v, ok := attr(t, "outline-level"); ok {
					if n, err := strconv.Atoi(v); err == nil {
						lv = max(1, min(6, n))
					}
				}
				stack = append(stack, &blk{tag: "h" + strconv.Itoa(lv)})
			case "p":
				stack = append(stack, &blk{tag: "p"})
			case "list-item":
				depth++
			case "s", "tab":
				if len(stack) > 0 {
					stack[len(stack)-1].b.WriteString(" ")
				}
			case "line-break":
				if len(stack) > 0 {
					stack[len(stack)-1].b.WriteString("<br>")
				}
			}
		case xml.CharData:
			if len(stack) > 0 {
				stack[len(stack)-1].b.WriteString(esc(string(t)))
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "h", "p":
				if len(stack) == 0 {
					continue
				}
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				s := top.b.String()
				if strings.TrimSpace(s) == "" {
					continue
				}
				if len(stack) > 0 {
					stack[len(stack)-1].b.WriteString(s)
				} else if top.tag == "p" && depth > 0 {
					out.WriteString("<p>• " + s + "</p>\n")
				} else {
					fmt.Fprintf(&out, "<%s>%s</%s>\n", top.tag, s, top.tag)
				}
			case "list-item":
				depth--
			}
		}
	}
	return out.String(), nil
}

var (
	fullPathRe = regexp.MustCompile(`full-path="([^"]+)"`)
	itemRe     = regexp.MustCompile(`<item\b[^>]*>`)
	idAttrRe   = regexp.MustCompile(`\bid="([^"]+)"`)
	hrefAttrRe = regexp.MustCompile(`\bhref="([^"]+)"`)
	itemrefRe  = regexp.MustCompile(`<itemref\b[^>]*idref="([^"]+)"`)
)

// epubHTML reads an EPUB's chapters in reading order.
func epubHTML(z *zip.Reader) (string, error) {
	container, ok := zipRead(z, "META-INF/container.xml")
	if !ok {
		return "", cvError("This doesn't look like an EPUB ebook.")
	}
	m := fullPathRe.FindSubmatch(container)
	if m == nil {
		return "", cvError("This EPUB is damaged.")
	}
	opfPath := string(m[1])
	opf, ok := zipRead(z, opfPath)
	if !ok {
		return "", cvError("This EPUB is damaged.")
	}
	baseDir := ""
	if i := strings.LastIndex(opfPath, "/"); i >= 0 {
		baseDir = opfPath[:i+1]
	}
	manifest := map[string]string{}
	for _, tag := range itemRe.FindAllString(string(opf), -1) {
		id, href := idAttrRe.FindStringSubmatch(tag), hrefAttrRe.FindStringSubmatch(tag)
		if id != nil && href != nil {
			manifest[id[1]] = href[1]
		}
	}
	var out strings.Builder
	for _, ref := range itemrefRe.FindAllStringSubmatch(string(opf), -1) {
		href, ok := manifest[ref[1]]
		if !ok {
			continue
		}
		href = strings.SplitN(href, "#", 2)[0]
		if u, err := url.PathUnescape(href); err == nil {
			href = u
		}
		if b, ok := zipRead(z, baseDir+href); ok {
			out.WriteString(cleanHTML(string(b)) + "\n")
		}
	}
	return out.String(), nil
}

var (
	mdImgRe    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLinkRe   = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)[^)]*\)`)
	mdCodeRe   = regexp.MustCompile("`([^`]+)`")
	mdBoldRe   = regexp.MustCompile(`\*\*([^*]+)\*\*|__([^_]+)__`)
	mdItalRe   = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*]*)\*|(^|[^_\w])_([^_\s][^_]*)_`)
	mdHeadRe   = regexp.MustCompile(`^(#{1,6})\s+(.*)$`)
	mdListRe   = regexp.MustCompile(`^([-*+]|\d+[.)])\s+(.*)$`)
	mdRuleRe   = regexp.MustCompile(`^(-{3,}|\*{3,}|_{3,})$`)
	safeHrefRe = regexp.MustCompile(`(?i)^(https?:|mailto:|#)`)
)

// markdownHTML is a small Markdown reader: headings, lists, quotes, code, bold, italic, links.
func markdownHTML(md string) string {
	var out strings.Builder
	var para []string
	var code *strings.Builder
	inline := func(s string) string {
		s = esc(s)
		s = mdImgRe.ReplaceAllString(s, "")
		s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
			p := mdLinkRe.FindStringSubmatch(m)
			if !safeHrefRe.MatchString(p[2]) {
				return p[1]
			}
			return `<a href="` + p[2] + `">` + p[1] + `</a>`
		})
		s = mdCodeRe.ReplaceAllString(s, "<code>$1</code>")
		s = mdBoldRe.ReplaceAllString(s, "<b>$1$2</b>")
		s = mdItalRe.ReplaceAllString(s, "$1$3<i>$2$4</i>")
		return s
	}
	flush := func() {
		if len(para) > 0 {
			parts := make([]string, len(para))
			for i, p := range para {
				parts[i] = inline(p)
			}
			out.WriteString("<p>" + strings.Join(parts, " ") + "</p>\n")
			para = nil
		}
	}
	for _, raw := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		line := strings.TrimRight(raw, " \t")
		if code != nil {
			if strings.HasPrefix(strings.TrimSpace(line), "```") {
				out.WriteString("<pre>" + esc(strings.TrimRight(code.String(), "\n")) + "</pre>\n")
				code = nil
			} else {
				code.WriteString(raw + "\n")
			}
			continue
		}
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "```"):
			flush()
			code = &strings.Builder{}
		case t == "":
			flush()
		case mdHeadRe.MatchString(t):
			flush()
			m := mdHeadRe.FindStringSubmatch(t)
			n := len(m[1])
			fmt.Fprintf(&out, "<h%d>%s</h%d>\n", n, inline(strings.TrimSpace(strings.TrimRight(m[2], "#"))), n)
		case mdRuleRe.MatchString(t):
			flush()
			out.WriteString("<hr>\n")
		case mdListRe.MatchString(t):
			flush()
			m := mdListRe.FindStringSubmatch(t)
			marker := "•"
			if m[1][0] >= '0' && m[1][0] <= '9' {
				marker = m[1]
			}
			out.WriteString("<p>" + marker + " " + inline(m[2]) + "</p>\n")
		case strings.HasPrefix(t, ">"):
			flush()
			out.WriteString("<blockquote>" + inline(strings.TrimSpace(strings.TrimLeft(t, ">"))) + "</blockquote>\n")
		default:
			para = append(para, t)
		}
	}
	if code != nil {
		out.WriteString("<pre>" + esc(code.String()) + "</pre>\n")
	}
	flush()
	return out.String()
}

// ---------- writers ----------

func htmlPage(title, body, extraCSS string) string {
	return `<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>` + esc(title) + `</title>
<style>body{font-family:system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans","Noto Sans Bengali",sans-serif;max-width:46em;margin:2em auto;padding:0 1em;line-height:1.6;color:#17181c}
h1,h2,h3{line-height:1.25}pre{background:#f4f5f7;padding:1em;overflow:auto;white-space:pre-wrap}blockquote{border-left:3px solid #ccc;margin-left:0;padding-left:1em;color:#555}` + extraCSS + `</style>
</head><body>
` + body + `
</body></html>
`
}

// findBrowser finds an installed Chrome-family browser for printing pages to PDF.
func findBrowser() string {
	var c []string
	switch runtime.GOOS {
	case "darwin":
		home, _ := os.UserHomeDir()
		for _, app := range []string{"Google Chrome.app/Contents/MacOS/Google Chrome", "Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Chromium.app/Contents/MacOS/Chromium", "Brave Browser.app/Contents/MacOS/Brave Browser", "Google Chrome Beta.app/Contents/MacOS/Google Chrome Beta",
			"Vivaldi.app/Contents/MacOS/Vivaldi", "Opera.app/Contents/MacOS/Opera"} {
			c = append(c, "/Applications/"+app, filepath.Join(home, "Applications", app))
		}
	case "windows":
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LocalAppData"} {
			if root := os.Getenv(env); root != "" {
				c = append(c, filepath.Join(root, `Google\Chrome\Application\chrome.exe`), filepath.Join(root, `Microsoft\Edge\Application\msedge.exe`),
					filepath.Join(root, `BraveSoftware\Brave-Browser\Application\brave.exe`))
			}
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "brave-browser"} {
			if p, err := exec.LookPath(n); err == nil {
				c = append(c, p)
			}
		}
	}
	for _, p := range c {
		if isFile(p) {
			return p
		}
	}
	return ""
}

// htmlToPdf prints the document with the installed browser. Text stays real text (searchable, copyable)
// and every script (Bangla, Arabic, Hindi...) is shaped properly. opts: page (a4, letter), font (small, large).
func (j *cvJob) htmlToPdf(title, body, out string) error {
	browser := findBrowser()
	if browser == "" {
		return cvError("Making a PDF needs Google Chrome or Microsoft Edge on this computer. Install one, or pick another format.")
	}
	page := "A4"
	if j.spec.opt("page") == "letter" {
		page = "Letter"
	}
	size := map[string]string{"small": "10pt", "large": "14pt"}[j.spec.opt("font")]
	if size == "" {
		size = "12pt"
	}
	css := "@page{size:" + page + ";margin:20mm}body{max-width:none;margin:0;padding:0;font-size:" + size + "}pre{background:none;border:1px solid #ddd}"
	src := j.tmp("doc.html")
	if err := os.WriteFile(src, []byte(htmlPage(title, body, css)), 0o644); err != nil {
		return err
	}
	profile := j.tmp("browser-profile")
	tmpOut := j.tmp("doc.pdf")
	ctx, cancel := context.WithTimeout(j.ctx, 3*time.Minute)
	defer cancel()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(src)}
	if runtime.GOOS == "windows" {
		u.Path = "/" + u.Path
	}
	cmd := exec.CommandContext(ctx, browser, "--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--password-store=basic", "--use-mock-keychain", "--user-data-dir="+profile, "--no-pdf-header-footer", "--print-to-pdf="+tmpOut, u.String())
	setProcAttrs(cmd)
	hideWindow(cmd)
	var outBuf tailBuffer
	cmd.Stdout, cmd.Stderr = &outBuf, &outBuf
	err := cmd.Start()
	if err == nil {
		// Chrome can take a while to close after writing the PDF; stop it once the file is whole.
		waited := make(chan error, 1)
		go func() { waited <- cmd.Wait() }()
		tick := time.NewTicker(150 * time.Millisecond)
	loop:
		for {
			select {
			case err = <-waited:
				break loop
			case <-tick.C:
				if pdfComplete(tmpOut) {
					time.Sleep(200 * time.Millisecond)
					killTree(cmd)
					<-waited
					err = nil
					break loop
				}
			}
		}
		tick.Stop()
	}
	outb := outBuf.String()
	if j.ctx.Err() != nil {
		return errCancelled
	}
	if err != nil && pdfComplete(tmpOut) {
		err = nil // Chrome sometimes fails while closing, after the PDF is fully written
	}
	if err != nil || !pdfComplete(tmpOut) {
		logf("print to pdf failed: %v: %s", err, outb)
		return cvError("Couldn't make the PDF. Close and reopen your browser, then try again.")
	}
	j.progress(95)
	if err := copyFile(tmpOut, out); err != nil {
		return err
	}
	return nil
}

var (
	brRe2    = regexp.MustCompile(`(?i)<br\s*/?>`)
	hrRe2    = regexp.MustCompile(`(?i)<hr\s*/?>`)
	h12Re    = regexp.MustCompile(`(?i)<h([12])>`)
	tocRe    = regexp.MustCompile(`<h[12] id="(s\d+)">(.*?)</h[12]>`)
	entityRe = regexp.MustCompile(`&([a-zA-Z][a-zA-Z0-9]*);`)
)

func wellFormed(fragment string) bool {
	d := xml.NewDecoder(strings.NewReader("<body>" + fragment + "</body>"))
	d.Strict = true
	for {
		_, err := d.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
	}
}

// writeEpub makes a valid EPUB 3 ebook with one chapter and a chapter list from the headings.
func writeEpub(title, body, out string) error {
	n := 0
	tidy := brRe2.ReplaceAllString(body, "<br/>")
	tidy = hrRe2.ReplaceAllString(tidy, "<hr/>")
	tidy = imgRe.ReplaceAllString(tidy, "")
	// XHTML only knows the five XML entities; write the rest as characters.
	tidy = entityRe.ReplaceAllStringFunc(tidy, func(m string) string {
		switch m {
		case "&amp;", "&lt;", "&gt;", "&quot;", "&apos;":
			return m
		}
		return esc(html.UnescapeString(m))
	})
	tidy = h12Re.ReplaceAllStringFunc(tidy, func(m string) string {
		n++
		return fmt.Sprintf(`<h%s id="s%d">`, m[2:3], n)
	})
	toc := ""
	if wellFormed(tidy) {
		for _, m := range tocRe.FindAllStringSubmatch(tidy, -1) {
			toc += `<li><a href="chapter.xhtml#` + m[1] + `">` + esc(htmlToText(m[2])) + `</a></li>`
		}
	} else {
		tidy = strings.ReplaceAll(textToHTML(htmlToText(body)), "<br>", "<br/>")
	}
	t := esc(title)
	if toc == "" {
		toc = `<li><a href="chapter.xhtml">` + t + `</a></li>`
	}
	idb := make([]byte, 16)
	rand.Read(idb)
	h := hex.EncodeToString(idb)
	uid := "urn:uuid:" + h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
	modified := time.Now().UTC().Format("2006-01-02T15:04:05Z")

	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	z := zip.NewWriter(f)
	mime := []byte("application/epub+zip")
	w, _ := z.CreateHeader(&zip.FileHeader{Name: "mimetype", Method: zip.Store, CRC32: crc32.ChecksumIEEE(mime),
		CompressedSize64: uint64(len(mime)), UncompressedSize64: uint64(len(mime))})
	w.Write(mime)
	add := func(name, text string) {
		w, _ := z.Create(name)
		io.WriteString(w, text)
	}
	add("META-INF/container.xml", `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles>
<rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	add("OEBPS/content.opf", `<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="uid">
<metadata xmlns:dc="http://purl.org/dc/elements/1.1/"><dc:identifier id="uid">`+uid+`</dc:identifier><dc:title>`+t+`</dc:title>
<dc:language>en</dc:language><meta property="dcterms:modified">`+modified+`</meta></metadata>
<manifest><item id="nav" href="nav.xhtml" media-type="application/xhtml+xml" properties="nav"/>
<item id="c1" href="chapter.xhtml" media-type="application/xhtml+xml"/></manifest>
<spine><itemref idref="c1"/></spine></package>`)
	add("OEBPS/nav.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops"><head><title>`+t+`</title></head>
<body><nav epub:type="toc"><ol>`+toc+`</ol></nav></body></html>`)
	add("OEBPS/chapter.xhtml", `<?xml version="1.0" encoding="UTF-8"?>
<html xmlns="http://www.w3.org/1999/xhtml"><head><title>`+t+`</title></head><body>
`+tidy+`
</body></html>`)
	return z.Close()
}

// pdfComplete says whether a file is a whole PDF (starts with %PDF, ends with %%EOF).
func pdfComplete(p string) bool {
	b, err := os.ReadFile(p)
	if err != nil || len(b) < 16 || !bytes.HasPrefix(b, []byte("%PDF")) {
		return false
	}
	tail := b[max(0, len(b)-64):]
	return bytes.Contains(tail, []byte("%%EOF"))
}
