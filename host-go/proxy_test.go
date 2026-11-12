package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync/atomic"
	"testing"

	"vidgrab-host/engine"
	"vidgrab-host/torture"
)

func TestParseScutilProxy(t *testing.T) {
	p := parseScutilProxy(`<dictionary> {
  ExceptionsList : <array> {
    0 : *.local
    1 : 169.254/16
    2 : intranet.example
  }
  FTPPassive : 1
  HTTPEnable : 1
  HTTPPort : 8080
  HTTPProxy : 10.0.0.5
  HTTPSEnable : 1
  HTTPSPort : 8443
  HTTPSProxy : proxy.corp
  SOCKSEnable : 0
  SOCKSPort : 1080
  SOCKSProxy : 10.0.0.6
}`)
	if p.HTTP != "10.0.0.5:8080" || p.HTTPS != "proxy.corp:8443" || p.SOCKS != "" || len(p.Bypass) != 3 {
		t.Fatalf("parsed wrong: %+v", p)
	}
	check := func(target, want string) {
		t.Helper()
		u, _ := url.Parse(target)
		got := ""
		if x := p.proxyURL(u); x != nil {
			got = x.String()
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", target, got, want)
		}
	}
	check("https://example.com/a", "http://proxy.corp:8443")
	check("http://example.com/a", "http://10.0.0.5:8080")
	check("http://localhost:3000/", "")
	check("http://127.0.0.1:3000/", "")
	check("https://printer.local/", "")
	check("https://wiki.intranet.example/", "")
	if q := parseScutilProxy("<dictionary> {\n  FTPPassive : 1\n}"); q.HTTP != "" || q.proxyURL(&url.URL{Scheme: "https", Host: "example.com"}) != nil {
		t.Errorf("no proxy configured should mean direct: %+v", q)
	}
}

func TestParseWindowsProxy(t *testing.T) {
	one := parseWindowsProxy("HKEY_CURRENT_USER\\...\\Internet Settings\r\n    ProxyEnable    REG_DWORD    0x1\r\n    ProxyServer    REG_SZ    10.1.1.1:3128\r\n    ProxyOverride    REG_SZ    *.corp.example;<local>\r\n")
	if one.HTTP != "10.1.1.1:3128" || one.HTTPS != "10.1.1.1:3128" || len(one.Bypass) != 2 {
		t.Fatalf("single proxy parsed wrong: %+v", one)
	}
	if one.proxyURL(&url.URL{Scheme: "http", Host: "fileserver"}) != nil {
		t.Errorf("<local> should bypass plain host names")
	}
	per := parseWindowsProxy("    ProxyEnable    REG_DWORD    0x1\r\n    ProxyServer    REG_SZ    http=a:1;https=b:2;socks=c:3\r\n")
	if per.HTTP != "a:1" || per.HTTPS != "b:2" || per.SOCKS != "c:3" {
		t.Fatalf("per-protocol parsed wrong: %+v", per)
	}
	if off := parseWindowsProxy("    ProxyEnable    REG_DWORD    0x0\r\n    ProxyServer    REG_SZ    a:1\r\n"); off.HTTP != "" {
		t.Errorf("disabled proxy must be ignored: %+v", off)
	}
}

func TestProxySetting(t *testing.T) {
	for _, bad := range []string{"ftp://x:1", "http://nohost", "http://:8080", "socks4://h:1"} {
		if _, err := parseProxySetting(bad); err == nil {
			t.Errorf("%q should be refused", bad)
		}
	}
	if u, err := parseProxySetting("10.0.0.1:8080"); err != nil || u.String() != "http://10.0.0.1:8080" {
		t.Errorf("bare host:port: %v %v", u, err)
	}
	if got := ytdlpProxyArgs("socks5://127.0.0.1:1080"); len(got) != 2 || got[1] != "socks5://127.0.0.1:1080" {
		t.Errorf("yt-dlp args: %v", got)
	}
	if got := ytdlpProxyArgs("direct"); len(got) != 2 || got[1] != "" {
		t.Errorf("yt-dlp direct args: %v", got)
	}
	if got := ytdlpProxyArgs(""); got != nil {
		t.Errorf("yt-dlp system args: %v", got)
	}
}

// A whole download through a real (local) HTTP proxy: every request must pass through it.
func TestDownloadThroughProxy(t *testing.T) {
	srv := torture.NewServer(512 * 1024)
	defer srv.Close()
	var seen atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		if !r.URL.IsAbs() {
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		out, _ := http.NewRequestWithContext(r.Context(), r.Method, r.URL.String(), nil)
		out.Header = r.Header.Clone()
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, v := range resp.Header {
			w.Header()[k] = v
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxy.Close()

	res, err := engine.Download(context.Background(), engine.Options{
		URL: srv.URL + "/normal", DestDir: t.TempDir(), MinSplitSize: 32 * 1024,
		Proxy: proxyFor(proxy.URL),
	})
	if err != nil {
		t.Fatalf("download through the proxy failed: %v", err)
	}
	got, _ := os.ReadFile(res.FilePath)
	if string(got) != string(srv.Payload) {
		t.Fatalf("file through the proxy doesn't match")
	}
	if seen.Load() == 0 {
		t.Fatalf("the proxy saw no requests: the download went direct")
	}
	t.Logf("proxy handled %d requests", seen.Load())

	// "direct" must not touch the proxy.
	seen.Store(0)
	if _, err := engine.Download(context.Background(), engine.Options{
		URL: srv.URL + "/normal", DestDir: t.TempDir(), Proxy: proxyFor("direct"),
	}); err != nil || seen.Load() != 0 {
		t.Fatalf("direct: err=%v, proxy saw %d requests", err, seen.Load())
	}
}
