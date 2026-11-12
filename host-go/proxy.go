package main

// Proxy support. The setting (from the extension or the desktop app) is one of:
//
//	""  or "system"   use the computer's proxy settings (macOS Network settings, Windows
//	                  Internet Options), or the usual HTTP_PROXY/HTTPS_PROXY variables
//	"direct"          never use a proxy
//	a proxy address   http://host:port, https://host:port, socks5://host:port
//	                  (a bare host:port means http://host:port)
//
// Chrome starts the helper without the user's shell environment, so reading only the
// environment variables (Go's default) would miss the system proxy on most computers.

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// systemProxy is what the operating system says about proxies.
type systemProxy struct {
	HTTP, HTTPS, SOCKS string   // "host:port", empty when off
	Bypass             []string // hosts that must not go through the proxy
}

// parseProxySetting checks a proxy setting typed by the user. It returns the proxy URL for
// an explicit proxy, or nil for "system"/"direct"/empty.
func parseProxySetting(setting string) (*url.URL, error) {
	s := strings.TrimSpace(setting)
	switch strings.ToLower(s) {
	case "", "system", "direct", "none":
		return nil, nil
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("that proxy address isn't valid: %s", setting)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("proxy type %q isn't supported (use http, https or socks5)", u.Scheme)
	}
	if u.Port() == "" {
		return nil, fmt.Errorf("the proxy address needs a port, like %s:8080", u.Hostname())
	}
	return u, nil
}

// proxyFor returns the function Go's HTTP client uses to pick a proxy for each request.
func proxyFor(setting string) func(*http.Request) (*url.URL, error) {
	s := strings.ToLower(strings.TrimSpace(setting))
	if s == "direct" || s == "none" {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	if u, err := parseProxySetting(setting); err == nil && u != nil {
		return http.ProxyURL(u) // an explicit proxy is used for everything
	}
	return func(req *http.Request) (*url.URL, error) {
		if u, err := http.ProxyFromEnvironment(req); u != nil || err != nil {
			return u, err
		}
		return getSystemProxy().proxyURL(req.URL), nil
	}
}

// ytdlpProxyArgs gives yt-dlp the same choice. With "system" yt-dlp is left alone: it reads
// the system proxy itself.
func ytdlpProxyArgs(setting string) []string {
	s := strings.ToLower(strings.TrimSpace(setting))
	if s == "direct" || s == "none" {
		return []string{"--proxy", ""}
	}
	if u, err := parseProxySetting(setting); err == nil && u != nil {
		return []string{"--proxy", u.String()}
	}
	return nil
}

// proxyURL picks the system proxy for a target, or nil for a direct connection.
func (p systemProxy) proxyURL(target *url.URL) *url.URL {
	host := strings.ToLower(target.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".local") {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
		return nil
	}
	for _, b := range p.Bypass {
		b = strings.ToLower(strings.TrimSpace(b))
		switch {
		case b == "":
		case b == "<local>":
			if !strings.Contains(host, ".") {
				return nil
			}
		case strings.HasPrefix(b, "*."):
			if strings.HasSuffix(host, b[1:]) || host == b[2:] {
				return nil
			}
		case strings.Contains(b, "/"):
			if _, cidr, err := net.ParseCIDR(b); err == nil {
				if ip := net.ParseIP(host); ip != nil && cidr.Contains(ip) {
					return nil
				}
			}
		case host == b || strings.HasSuffix(host, "."+b):
			return nil
		}
	}
	addr := p.HTTP
	if target.Scheme == "https" && p.HTTPS != "" {
		addr = p.HTTPS
	}
	if addr != "" {
		return &url.URL{Scheme: "http", Host: addr}
	}
	if p.SOCKS != "" {
		return &url.URL{Scheme: "socks5", Host: p.SOCKS}
	}
	return nil
}

var (
	sysProxyMu   sync.Mutex
	sysProxyVal  systemProxy
	sysProxyRead time.Time
)

// getSystemProxy reads the system proxy settings, at most once a minute.
func getSystemProxy() systemProxy {
	sysProxyMu.Lock()
	defer sysProxyMu.Unlock()
	if !sysProxyRead.IsZero() && time.Since(sysProxyRead) < time.Minute {
		return sysProxyVal
	}
	sysProxyRead = time.Now()
	sysProxyVal = systemProxy{}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	switch runtime.GOOS {
	case "darwin":
		if out, err := exec.CommandContext(ctx, "scutil", "--proxy").Output(); err == nil {
			sysProxyVal = parseScutilProxy(string(out))
		}
	case "windows":
		cmd := command("reg", "query", `HKCU\Software\Microsoft\Windows\CurrentVersion\Internet Settings`)
		done := make(chan []byte, 1)
		go func() { out, _ := cmd.Output(); done <- out }()
		select {
		case out := <-done:
			sysProxyVal = parseWindowsProxy(string(out))
		case <-ctx.Done():
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		}
	}
	return sysProxyVal
}

var scutilLineRe = regexp.MustCompile(`(?m)^\s*(\w+)\s*:\s*(.+?)\s*$`)

// parseScutilProxy reads the output of macOS `scutil --proxy`.
func parseScutilProxy(text string) systemProxy {
	kv := map[string]string{}
	var p systemProxy
	inExceptions := false
	for _, line := range strings.Split(text, "\n") {
		m := scutilLineRe.FindStringSubmatch(line)
		if m == nil {
			if strings.Contains(line, "}") {
				inExceptions = false
			}
			continue
		}
		key, val := m[1], m[2]
		if key == "ExceptionsList" {
			inExceptions = true
			continue
		}
		if inExceptions && regexp.MustCompile(`^\d+$`).MatchString(key) {
			p.Bypass = append(p.Bypass, val)
			continue
		}
		kv[key] = val
	}
	pick := func(prefix string) string {
		if kv[prefix+"Enable"] != "1" || kv[prefix+"Proxy"] == "" || kv[prefix+"Port"] == "" {
			return ""
		}
		return net.JoinHostPort(kv[prefix+"Proxy"], kv[prefix+"Port"])
	}
	p.HTTP, p.HTTPS, p.SOCKS = pick("HTTP"), pick("HTTPS"), pick("SOCKS")
	return p
}

var regValueRe = regexp.MustCompile(`(?m)^\s*(\w+)\s+REG_\w+\s+(.*?)\s*$`)

// parseWindowsProxy reads `reg query` output of the Internet Settings key.
func parseWindowsProxy(text string) systemProxy {
	kv := map[string]string{}
	for _, m := range regValueRe.FindAllStringSubmatch(text, -1) {
		kv[m[1]] = m[2]
	}
	var p systemProxy
	if kv["ProxyEnable"] != "0x1" || kv["ProxyServer"] == "" {
		return p
	}
	server := kv["ProxyServer"]
	if strings.Contains(server, "=") { // "http=host:port;https=host:port;socks=host:port"
		for _, part := range strings.Split(server, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if !ok {
				continue
			}
			v = strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "socks://")
			switch strings.ToLower(k) {
			case "http":
				p.HTTP = v
			case "https":
				p.HTTPS = v
			case "socks":
				p.SOCKS = v
			}
		}
	} else { // one proxy for everything
		p.HTTP, p.HTTPS = server, server
	}
	for _, b := range strings.Split(kv["ProxyOverride"], ";") {
		if b = strings.TrimSpace(b); b != "" {
			p.Bypass = append(p.Bypass, b)
		}
	}
	return p
}
