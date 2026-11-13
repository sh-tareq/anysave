package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

// A small website: a start page, two levels of sub-pages, files at every level, a part
// robots.txt forbids, a link to another website and a page outside the start folder.
func crawlTestSite(t *testing.T, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	pages := map[string]string{
		"/robots.txt":            "User-agent: *\nDisallow: /docs/private/\n",
		"/docs/":                 `<a href="a.pdf">A</a> <a href='sub/'>sub</a> <a href=/docs/private/>private</a> <a href="/other/">other</a> <a href="https://elsewhere.invalid/x.zip">off-site file</a> <a href="https://elsewhere.invalid/page/">off-site page</a> <img src="/img/logo.png"> <a href="style.css">css</a> <a href="a.pdf#page=2">again</a>`,
		"/docs/sub/":             `<a href="b.zip">B</a> <a href="deep/">deep</a> <a href="../">up</a>`,
		"/docs/sub/deep/":        `<a href="c.mp4">C</a> <a href="deeper/">deeper</a>`,
		"/docs/sub/deep/deeper/": `<a href="d.iso">D</a>`,
		"/docs/private/":         `<a href="secret.pdf">secret</a>`,
		"/other/":                `<a href="e.pdf">E</a>`,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		body, ok := pages[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/robots.txt" {
			w.Header().Set("Content-Type", "text/plain")
		} else {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		fmt.Fprint(w, body)
	}))
}

func crawlNames(res map[string]any) []string {
	var out []string
	files, _ := res["files"].([]crawlFile)
	for _, f := range files {
		out = append(out, f.Name)
	}
	sort.Strings(out)
	return out
}

func TestCrawl(t *testing.T) {
	var hits atomic.Int32
	srv := crawlTestSite(t, &hits)
	defer srv.Close()
	run := func(extra map[string]any) map[string]any {
		msg := map[string]any{"action": "crawl", "url": srv.URL + "/docs/", "pauseMs": float64(0)}
		for k, v := range extra {
			msg[k] = v
		}
		res := actionCrawl(msg)
		if res["ok"] != true {
			t.Fatalf("crawl failed: %v", res)
		}
		return res
	}
	check := func(name string, res map[string]any, want string) {
		t.Helper()
		if got := strings.Join(crawlNames(res), " "); got != want {
			t.Errorf("%s: got [%s], want [%s]", name, got, want)
		}
	}
	// This page only: its own files, also ones hosted elsewhere; no sub-pages are read.
	check("depth 0", run(map[string]any{"depth": float64(0)}), "a.pdf logo.png x.zip")
	// One level: the sub-page and /other/, but never the part robots.txt forbids.
	check("depth 1", run(map[string]any{"depth": float64(1)}), "a.pdf b.zip e.pdf logo.png x.zip")
	check("depth 2", run(map[string]any{"depth": float64(2)}), "a.pdf b.zip c.mp4 e.pdf logo.png x.zip")
	check("depth 3", run(map[string]any{"depth": float64(3)}), "a.pdf b.zip c.mp4 d.iso e.pdf logo.png x.zip")
	check("depth is capped at 3", run(map[string]any{"depth": float64(9)}), "a.pdf b.zip c.mp4 d.iso e.pdf logo.png x.zip")
	// Only under the start folder.
	check("same folder only", run(map[string]any{"depth": float64(3), "samePath": true}), "a.pdf b.zip c.mp4 d.iso logo.png x.zip")
	// File type filter.
	check("types", run(map[string]any{"depth": float64(3), "types": "pdf, .ZIP"}), "a.pdf b.zip e.pdf x.zip")
	// Page limit is respected and reported.
	res := run(map[string]any{"depth": float64(3), "maxPages": float64(2)})
	if res["pages"].(int) > 2 || res["stopped"] != "page limit" {
		t.Errorf("page limit: pages=%v stopped=%q", res["pages"], res["stopped"])
	}
	for _, f := range run(map[string]any{"depth": float64(3)})["files"].([]crawlFile) {
		if f.Name == "secret.pdf" {
			t.Errorf("crawled a page robots.txt forbids")
		}
		if f.From == "" || f.URL == "" || f.Ext == "" {
			t.Errorf("incomplete entry: %+v", f)
		}
	}
	if bad := actionCrawl(map[string]any{"url": "ftp://x/"}); bad["ok"] != false {
		t.Errorf("non-web link accepted")
	}
	if dead := actionCrawl(map[string]any{"url": srv.URL + "/nope/"}); dead["ok"] != false {
		t.Errorf("unreadable page should be an error, got %v", dead)
	}
}
