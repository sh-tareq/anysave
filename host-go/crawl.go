package main

// Site crawler: starting from one page, follow the links of the same website a few levels deep
// and list the files it links to, so the user can pick which ones to download.
//
//	{"action":"crawl","url":"https://example.com/downloads/","depth":1,"types":"pdf,zip",
//	 "samePath":true,"maxPages":150}
//	-> {"ok":true,"files":[{"url","name","ext","from"}...],"pages":37,"stopped":""}
//
// It is deliberately gentle: it stays on the same website, reads robots.txt, asks for one page
// at a time per connection with a small pause, and stops after a page limit and a time limit.

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	crawlMaxDepth   = 3
	crawlMaxPages   = 300
	crawlMaxFiles   = 2000
	crawlPageBytes  = 2 * 1024 * 1024
	crawlWorkers    = 3
	crawlPause      = 150 * time.Millisecond
	crawlTimeLimit  = 50 * time.Second
	crawlPageTimout = 12 * time.Second
)

var (
	// href="..." / src="..." / data-href='...' in HTML.
	crawlLinkRe = regexp.MustCompile(`(?i)(?:href|src|data-href|data-url|data-src)\s*=\s*(?:"([^"]+)"|'([^']+)'|([^\s"'<>]+))`)
	crawlBaseRe = regexp.MustCompile(`(?i)<base[^>]+href\s*=\s*["']?([^"'\s>]+)`)
	// Extensions that are files to offer (the same set the extension treats as files).
	crawlFileExt = map[string]bool{}
	// Extensions that are certainly not pages worth reading.
	crawlSkipExt = map[string]bool{"css": true, "js": true, "mjs": true, "json": true, "xml": true, "rss": true, "ico": true,
		"woff": true, "woff2": true, "ttf": true, "otf": true, "eot": true, "map": true}
)

func init() {
	for _, e := range strings.Fields("mp4 mkv webm mov avi m4v 3gp flv wmv ts mpg mpeg mp3 m4a aac wav flac ogg opus wma " +
		"jpg jpeg png gif webp heic bmp svg tif tiff pdf doc docx xls xlsx ppt pptx txt csv rtf odt ods epub " +
		"zip rar 7z tar gz tgz bz2 xz iso dmg apk exe msi pkg deb rpm appimage bin img") {
		crawlFileExt[e] = true
	}
}

type crawlFile struct {
	URL  string `json:"url"`
	Name string `json:"name"`
	Ext  string `json:"ext"`
	From string `json:"from"`
}

type crawlOptions struct {
	Start     *url.URL
	Depth     int
	MaxPages  int
	Types     map[string]bool // empty = every known file type
	SamePath  bool            // only follow pages under the start page's folder
	UserAgent string
	Client    *http.Client
	Pause     time.Duration
	TimeLimit time.Duration
}

type crawlResult struct {
	Files   []crawlFile
	Pages   int
	Stopped string // why it ended early ("" = it read everything)
}

func extOfPath(p string) string {
	e := strings.ToLower(strings.TrimPrefix(path.Ext(p), "."))
	if len(e) > 8 {
		return ""
	}
	return e
}

// sameSite: the same host, ignoring a leading "www.".
func sameSite(a, b *url.URL) bool {
	strip := func(h string) string { return strings.TrimPrefix(strings.ToLower(h), "www.") }
	return strip(a.Hostname()) == strip(b.Hostname()) && a.Port() == b.Port()
}

// robotsDisallow reads the Disallow rules that apply to every client ("User-agent: *") or to us.
func robotsDisallow(body string) []string {
	var rules []string
	applies := false
	sawAgent := false
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "#"); i >= 0 {
			line = line[:i]
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if !sawAgent {
				applies = false // a new group starts
			}
			sawAgent = true
			if v == "*" || strings.Contains(strings.ToLower(v), "anysave") {
				applies = true
			}
		case "disallow":
			sawAgent = false
			if applies && v != "" {
				rules = append(rules, v)
			}
		default:
			sawAgent = false
		}
	}
	return rules
}

func robotsBlocks(rules []string, u *url.URL) bool {
	p := u.EscapedPath()
	if p == "" {
		p = "/"
	}
	for _, r := range rules {
		r = strings.TrimSuffix(r, "*")
		if strings.HasPrefix(p, r) {
			return true
		}
	}
	return false
}

// crawlSite does the work. It never leaves the start page's website.
func crawlSite(ctx context.Context, o crawlOptions) crawlResult {
	if o.Depth < 0 {
		o.Depth = 0
	}
	if o.Depth > crawlMaxDepth {
		o.Depth = crawlMaxDepth
	}
	if o.MaxPages <= 0 || o.MaxPages > crawlMaxPages {
		o.MaxPages = crawlMaxPages
	}
	if o.TimeLimit <= 0 {
		o.TimeLimit = crawlTimeLimit
	}
	ctx, cancel := context.WithTimeout(ctx, o.TimeLimit)
	defer cancel()

	get := func(u string) (*http.Response, error) {
		rctx, rcancel := context.WithTimeout(ctx, crawlPageTimout)
		req, err := http.NewRequestWithContext(rctx, http.MethodGet, u, nil)
		if err != nil {
			rcancel()
			return nil, err
		}
		req.Header.Set("User-Agent", o.UserAgent)
		req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.5")
		resp, err := o.Client.Do(req)
		if err != nil {
			rcancel()
			return nil, err
		}
		resp.Body = &cancelOnClose{ReadCloser: resp.Body, cancel: rcancel}
		return resp, nil
	}

	var disallow []string
	if resp, err := get(o.Start.Scheme + "://" + o.Start.Host + "/robots.txt"); err == nil {
		if resp.StatusCode == http.StatusOK {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
			disallow = robotsDisallow(string(b))
		}
		resp.Body.Close()
	}

	baseDir := o.Start.EscapedPath()
	if i := strings.LastIndex(baseDir, "/"); i >= 0 {
		baseDir = baseDir[:i+1]
	} else {
		baseDir = "/"
	}

	type page struct {
		u     *url.URL
		depth int
	}
	var (
		mu       sync.Mutex
		seenPage = map[string]bool{}
		seenFile = map[string]bool{}
		files    []crawlFile
		pages    int
		stopped  string
		pending  int
		queue    []page
		cond     = sync.NewCond(&mu)
	)
	key := func(u *url.URL) string {
		c := *u
		c.Fragment = ""
		return c.String()
	}
	enqueue := func(u *url.URL, depth int) { // call with mu held
		k := key(u)
		if seenPage[k] || len(seenPage) >= o.MaxPages {
			if !seenPage[k] && stopped == "" {
				stopped = "page limit"
			}
			return
		}
		seenPage[k] = true
		queue = append(queue, page{u, depth})
		pending++
		cond.Signal()
	}

	handle := func(pg page) {
		resp, err := get(pg.u.String())
		if err != nil {
			return
		}
		defer resp.Body.Close()
		ctype := strings.ToLower(resp.Header.Get("Content-Type"))
		if resp.StatusCode != http.StatusOK || !(strings.Contains(ctype, "html") || ctype == "") {
			return
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, crawlPageBytes))
		html := string(body)
		base := resp.Request.URL // after redirects
		if !sameSite(base, o.Start) {
			return
		}
		if m := crawlBaseRe.FindStringSubmatch(html); m != nil {
			if b, err := base.Parse(m[1]); err == nil {
				base = b
			}
		}
		mu.Lock()
		pages++
		mu.Unlock()
		for _, m := range crawlLinkRe.FindAllStringSubmatch(html, -1) {
			raw := m[1] + m[2] + m[3]
			raw = strings.TrimSpace(strings.ReplaceAll(raw, "&amp;", "&"))
			if raw == "" || strings.HasPrefix(raw, "#") {
				continue
			}
			u, err := base.Parse(raw)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
				continue
			}
			ext := extOfPath(u.Path)
			if crawlFileExt[ext] {
				if len(o.Types) > 0 && !o.Types[ext] {
					continue
				}
				mu.Lock()
				k := key(u)
				if !seenFile[k] {
					if len(files) >= crawlMaxFiles {
						stopped = "file limit"
					} else {
						seenFile[k] = true
						name, _ := url.PathUnescape(path.Base(u.Path))
						files = append(files, crawlFile{URL: k, Name: name, Ext: ext, From: pg.u.String()})
					}
				}
				mu.Unlock()
				continue
			}
			if pg.depth >= o.Depth || crawlSkipExt[ext] || !sameSite(u, o.Start) || robotsBlocks(disallow, u) {
				continue
			}
			if o.SamePath && !strings.HasPrefix(u.EscapedPath(), baseDir) {
				continue
			}
			mu.Lock()
			enqueue(u, pg.depth+1)
			mu.Unlock()
		}
	}

	mu.Lock()
	enqueue(o.Start, 0)
	mu.Unlock()

	var wg sync.WaitGroup
	for i := 0; i < crawlWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				mu.Lock()
				for len(queue) == 0 && pending > 0 && ctx.Err() == nil {
					cond.Wait()
				}
				if len(queue) == 0 || ctx.Err() != nil {
					mu.Unlock()
					cond.Broadcast()
					return
				}
				pg := queue[0]
				queue = queue[1:]
				mu.Unlock()

				handle(pg)
				select {
				case <-ctx.Done():
				case <-time.After(o.Pause):
				}

				mu.Lock()
				pending--
				if pending == 0 {
					cond.Broadcast()
				}
				mu.Unlock()
			}
		}()
	}
	// Wake the waiting workers when the time is up.
	go func() {
		<-ctx.Done()
		mu.Lock()
		cond.Broadcast()
		mu.Unlock()
	}()
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if ctx.Err() == context.DeadlineExceeded && pending > 0 {
		stopped = "time limit"
	}
	sort.Slice(files, func(i, j int) bool { return files[i].URL < files[j].URL })
	return crawlResult{Files: files, Pages: pages, Stopped: stopped}
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// actionCrawl is the helper action behind the manager's "Scan a website for files".
func actionCrawl(msg map[string]any) map[string]any {
	raw := strings.TrimSpace(str(msg, "url"))
	start, err := url.Parse(raw)
	if err != nil || (start.Scheme != "http" && start.Scheme != "https") || start.Host == "" {
		return map[string]any{"ok": false, "error": "That isn't a web link."}
	}
	depth := 0
	if d, ok := msg["depth"].(float64); ok {
		depth = int(d)
	}
	maxPages := 0
	if n, ok := msg["maxPages"].(float64); ok {
		maxPages = int(n)
	}
	types := map[string]bool{}
	for _, t := range strings.FieldsFunc(strings.ToLower(str(msg, "types")), func(r rune) bool { return r == ',' || r == ' ' || r == ';' }) {
		if t = strings.TrimPrefix(t, "."); t != "" {
			types[t] = true
		}
	}
	samePath, _ := msg["samePath"].(bool)
	pause := crawlPause
	if ms, ok := msg["pauseMs"].(float64); ok && ms >= 0 { // tests
		pause = time.Duration(ms) * time.Millisecond
	}
	res := crawlSite(context.Background(), crawlOptions{
		Start: start, Depth: depth, MaxPages: maxPages, Types: types, SamePath: samePath,
		UserAgent: fileUA, Pause: pause,
		Client: &http.Client{Transport: &http.Transport{Proxy: proxyFor(str(msg, "proxy"))}},
	})
	if res.Pages == 0 {
		return map[string]any{"ok": false, "error": "Couldn't read that page. Check the link, or the site may block automatic visitors."}
	}
	return map[string]any{"ok": true, "files": res.Files, "pages": res.Pages, "stopped": res.Stopped}
}
