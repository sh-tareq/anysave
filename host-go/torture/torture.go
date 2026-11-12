// Package torture provides an in-process HTTP test server that simulates real-world
// hostile, flaky, and misbehaving download servers.
//
// Scenarios covered:
//   - "/normal":               200/206 with Range, ETag, Last-Modified, and X-Content-SHA256
//   - "/no-range":             ignores Range headers and always sends 200 OK full body
//   - "/drop-midway":          drops TCP/HTTP stream halfway through the first N requests
//   - "/slow":                 throttles bandwidth to a configurable byte rate
//   - "/changing-etag":        mutates file content & ETag after the initial request (tests If-Range / ETag mismatch detection)
//   - "/bad-length-short":     declares a Content-Length larger than actual bytes sent
//   - "/no-length":            streams with chunked transfer encoding and no Content-Length
//   - "/redirect":             302 -> 301 -> 307 redirect chain before serving Range-capable content
//   - "/single-conn-only":     returns 403 Forbidden (or 429) if more than 1 concurrent connection is active
//   - "/corrupt-hash":         sends valid bytes but advertises a mismatched X-Content-SHA256 header
package torture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Server wraps an httptest.Server with deterministic test payloads and fault counters.
type Server struct {
	*httptest.Server
	Payload        []byte
	PayloadV2      []byte // mutated payload used by /changing-etag after mutation triggers
	SHA256         string
	SHA256V2       string
	ETag           string
	ETagV2         string
	LastMod        string
	DropCount      atomic.Int32 // number of mid-stream drops remaining on /drop-midway
	ActiveConns    atomic.Int32
	MaxSeenConn    atomic.Int32
	MutateAfter    atomic.Int32 // requests before /changing-etag switches to PayloadV2
	StallCount     atomic.Int32 // requests on /stall that still go silent after a few bytes
	LimitedMax     atomic.Int32 // most connections /max-3-conns served at once
	limited        atomic.Int32
	ReqLimited     atomic.Int32 // requests seen by /max-3-requests
	LimitedRefused atomic.Int32 // requests /max-3-conns answered with 429

	mu          sync.Mutex
	changeCalls int
}

// NewServer creates and starts a torture test server with a deterministic payload of sizeBytes.
func NewServer(sizeBytes int) *Server {
	if sizeBytes <= 0 {
		sizeBytes = 256 * 1024 // default 256 KB
	}
	payload := make([]byte, sizeBytes)
	for i := range payload {
		payload[i] = byte((i*31 + 17) & 0xff)
	}
	payloadV2 := make([]byte, sizeBytes)
	for i := range payloadV2 {
		payloadV2[i] = byte((i*73 + 99) & 0xff)
	}
	sum1 := sha256.Sum256(payload)
	sum2 := sha256.Sum256(payloadV2)

	s := &Server{
		Payload:   payload,
		PayloadV2: payloadV2,
		SHA256:    hex.EncodeToString(sum1[:]),
		SHA256V2:  hex.EncodeToString(sum2[:]),
		ETag:      `"anysave-v1-` + hex.EncodeToString(sum1[:6]) + `"`,
		ETagV2:    `"anysave-v2-` + hex.EncodeToString(sum2[:6]) + `"`,
		LastMod:   time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC).Format(http.TimeFormat),
	}
	s.DropCount.Store(2)
	s.MutateAfter.Store(1)

	mux := http.NewServeMux()
	mux.HandleFunc("/normal", s.handleNormal)
	mux.HandleFunc("/no-range", s.handleNoRange)
	mux.HandleFunc("/drop-midway", s.handleDropMidway)
	mux.HandleFunc("/slow", s.handleSlow)
	mux.HandleFunc("/changing-etag", s.handleChangingETag)
	mux.HandleFunc("/bad-length-short", s.handleBadLengthShort)
	mux.HandleFunc("/no-length", s.handleNoLength)
	mux.HandleFunc("/redirect", s.handleRedirect1)
	mux.HandleFunc("/redirect-2", s.handleRedirect2)
	mux.HandleFunc("/redirect-3", s.handleRedirect3)
	mux.HandleFunc("/single-conn-only", s.handleSingleConnOnly)
	mux.HandleFunc("/corrupt-hash", s.handleCorruptHash)
	mux.HandleFunc("/max-3-conns", s.handleMax3Conns)
	mux.HandleFunc("/stall", s.handleStall)
	mux.HandleFunc("/max-3-requests", func(w http.ResponseWriter, r *http.Request) {
		// Limits requests, not connections: the first 3 are served (slowly), every later
		// one is told to come back in 30 seconds. Like file hosts with a request budget.
		if s.ReqLimited.Add(1) > 3 {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		s.handleSlow(w, r)
	})
	mux.HandleFunc("/browsers-only", func(w http.ResponseWriter, r *http.Request) {
		// Refuses download tools by name, like some file hosts do.
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Mozilla/") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
	})
	mux.HandleFunc("/no-fake-browsers", func(w http.ResponseWriter, r *http.Request) {
		// Hangs up on anything that claims to be a browser, like some bot filters do.
		if strings.HasPrefix(r.Header.Get("User-Agent"), "Mozilla/") {
			if hj, ok := w.(http.Hijacker); ok {
				if c, _, err := hj.Hijack(); err == nil {
					c.Close()
					return
				}
			}
		}
		s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
	})

	s.Server = httptest.NewServer(mux)
	return s
}

func (s *Server) trackConn() func() {
	cur := s.ActiveConns.Add(1)
	for {
		prev := s.MaxSeenConn.Load()
		if cur <= prev || s.MaxSeenConn.CompareAndSwap(prev, cur) {
			break
		}
	}
	return func() { s.ActiveConns.Add(-1) }
}

func (s *Server) setStandardHeaders(w http.ResponseWriter, etag, sha string) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="testfile.bin"`)
	w.Header().Set("ETag", etag)
	w.Header().Set("Last-Modified", s.LastMod)
	if sha != "" {
		w.Header().Set("X-Content-SHA256", sha)
	}
}

// parseRange parses "bytes=start-end" against total length.
func parseRange(hdr string, total int) (start, end int, ok bool) {
	if !strings.HasPrefix(hdr, "bytes=") {
		return 0, 0, false
	}
	spec := strings.TrimPrefix(hdr, "bytes=")
	if strings.Contains(spec, ",") {
		return 0, 0, false
	}
	parts := strings.SplitN(spec, "-", 2)
	if len(parts) != 2 || parts[0] == "" {
		return 0, 0, false
	}
	st, err := strconv.Atoi(parts[0])
	if err != nil || st < 0 || st >= total {
		return 0, 0, false
	}
	en := total - 1
	if parts[1] != "" {
		e, err := strconv.Atoi(parts[1])
		if err != nil || e < st {
			return 0, 0, false
		}
		if e < total {
			en = e
		}
	}
	return st, en, true
}

func (s *Server) serveBytesWithRange(w http.ResponseWriter, r *http.Request, data []byte, etag, sha string) {
	s.setStandardHeaders(w, etag, sha)
	w.Header().Set("Accept-Ranges", "bytes")

	// Check If-Range: if client sends an old ETag that no longer matches, RFC 9110 says
	// return 200 OK with the full new representation.
	if ir := strings.TrimSpace(r.Header.Get("If-Range")); ir != "" && ir != etag && ir != s.LastMod {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
		return
	}

	if rng := r.Header.Get("Range"); rng != "" {
		st, en, ok := parseRange(rng, len(data))
		if !ok {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(data)))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		slice := data[st : en+1]
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", st, en, len(data)))
		w.Header().Set("Content-Length", strconv.Itoa(len(slice)))
		w.WriteHeader(http.StatusPartialContent)
		if r.Method != http.MethodHead {
			_, _ = w.Write(slice)
		}
		return
	}

	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

func (s *Server) handleNormal(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
}

func (s *Server) handleNoRange(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.setStandardHeaders(w, s.ETag, s.SHA256)
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.Payload)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.Payload)
	}
}

func (s *Server) handleDropMidway(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	if r.Method == http.MethodHead {
		s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
		return
	}
	// If DropCount > 0, cut the connection halfway through the requested range.
	if rem := s.DropCount.Add(-1); rem >= 0 {
		s.setStandardHeaders(w, s.ETag, s.SHA256)
		w.Header().Set("Accept-Ranges", "bytes")
		st, en := 0, len(s.Payload)-1
		status := http.StatusOK
		if rng := r.Header.Get("Range"); rng != "" {
			if a, b, ok := parseRange(rng, len(s.Payload)); ok {
				st, en = a, b
				status = http.StatusPartialContent
				w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", st, en, len(s.Payload)))
			}
		}
		fullLen := en - st + 1
		w.Header().Set("Content-Length", strconv.Itoa(fullLen))
		w.WriteHeader(status)
		half := fullLen / 2
		if half > 0 {
			_, _ = w.Write(s.Payload[st : st+half])
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hijack and close raw socket to simulate sudden TCP drop.
		if hj, ok := w.(http.Hijacker); ok {
			if conn, _, err := hj.Hijack(); err == nil {
				_ = conn.Close()
				return
			}
		}
		return
	}
	s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
}

func (s *Server) handleSlow(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.setStandardHeaders(w, s.ETag, s.SHA256)
	w.Header().Set("Accept-Ranges", "bytes")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.Itoa(len(s.Payload)))
		w.WriteHeader(http.StatusOK)
		return
	}
	st, en := 0, len(s.Payload)-1
	status := http.StatusOK
	if rng := r.Header.Get("Range"); rng != "" {
		if a, b, ok := parseRange(rng, len(s.Payload)); ok {
			st, en = a, b
			status = http.StatusPartialContent
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", st, en, len(s.Payload)))
		}
	}
	slice := s.Payload[st : en+1]
	w.Header().Set("Content-Length", strconv.Itoa(len(slice)))
	w.WriteHeader(status)
	chunk := 16 * 1024
	for off := 0; off < len(slice); off += chunk {
		end := off + chunk
		if end > len(slice) {
			end = len(slice)
		}
		if _, err := w.Write(slice[off:end]); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func (s *Server) handleChangingETag(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.mu.Lock()
	s.changeCalls++
	callNum := s.changeCalls
	s.mu.Unlock()

	threshold := int(s.MutateAfter.Load())
	if callNum <= threshold {
		s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
		return
	}
	// After threshold calls, the file on the server has been replaced with PayloadV2!
	s.serveBytesWithRange(w, r, s.PayloadV2, s.ETagV2, s.SHA256V2)
}

func (s *Server) handleBadLengthShort(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.setStandardHeaders(w, s.ETag, s.SHA256)
	// Lie about Content-Length (claim 5000 extra bytes), then close connection early.
	w.Header().Set("Content-Length", strconv.Itoa(len(s.Payload)+5000))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(s.Payload)
	}
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
		}
	}
}

func (s *Server) handleNoLength(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	s.setStandardHeaders(w, s.ETag, s.SHA256)
	// Force chunked transfer without Content-Length.
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = bytes.NewReader(s.Payload).WriteTo(w)
	}
}

func (s *Server) handleRedirect1(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/redirect-2", http.StatusFound)
}

func (s *Server) handleRedirect2(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/redirect-3", http.StatusMovedPermanently)
}

func (s *Server) handleRedirect3(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/normal", http.StatusTemporaryRedirect)
}

func (s *Server) handleSingleConnOnly(w http.ResponseWriter, r *http.Request) {
	cur := s.ActiveConns.Add(1)
	defer s.ActiveConns.Add(-1)
	if cur > 1 {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("403 Forbidden: only 1 connection allowed per client"))
		return
	}
	// Hold connection briefly so concurrent requests overlap reliably in tests.
	if r.Method != http.MethodHead {
		time.Sleep(25 * time.Millisecond)
	}
	s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
}

// handleMax3Conns serves at most 3 requests at a time and answers the rest with 429,
// like file hosts that limit connections per visitor.
func (s *Server) handleMax3Conns(w http.ResponseWriter, r *http.Request) {
	cur := s.limited.Add(1)
	defer s.limited.Add(-1)
	if cur > 3 {
		s.LimitedRefused.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	for {
		prev := s.LimitedMax.Load()
		if cur <= prev || s.LimitedMax.CompareAndSwap(prev, cur) {
			break
		}
	}
	time.Sleep(25 * time.Millisecond) // overlap the requests reliably
	s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
}

// handleStall sends the first bytes of the range and then goes silent without closing,
// for the first StallCount requests (a connection that died without telling anyone).
func (s *Server) handleStall(w http.ResponseWriter, r *http.Request) {
	if s.StallCount.Add(-1) < 0 {
		s.serveBytesWithRange(w, r, s.Payload, s.ETag, s.SHA256)
		return
	}
	start, end, ok := parseRange(r.Header.Get("Range"), len(s.Payload))
	if !ok {
		start, end = 0, len(s.Payload)-1
	}
	s.setStandardHeaders(w, s.ETag, s.SHA256)
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.Payload)))
	w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
	w.WriteHeader(http.StatusPartialContent)
	n := 4096
	if end-start+1 < n {
		n = end - start + 1
	}
	_, _ = w.Write(s.Payload[start : start+n])
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	select {
	case <-r.Context().Done():
	case <-time.After(30 * time.Second):
	}
}

func (s *Server) handleCorruptHash(w http.ResponseWriter, r *http.Request) {
	defer s.trackConn()()
	badHash := strings.Repeat("f", 64)
	s.serveBytesWithRange(w, r, s.Payload, s.ETag, badHash)
}
