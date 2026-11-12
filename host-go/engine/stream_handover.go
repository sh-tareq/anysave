package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// StartInfo is passed to Options.OnStart as soon as the remote server responds to
// the initial probe and the local part file is created.
type StartInfo struct {
	FileName     string
	TotalBytes   int64
	AcceptRanges bool
	PartPath     string
	// ProgressState returns the number of contiguous bytes written from offset 0,
	// whether the entire file has finished and passed SHA-256 integrity verification,
	// and any terminal error.
	ProgressState func() (contiguousBytes int64, verifiedDone bool, err error)
}

// LiveStreamServer serves an in-progress download over 127.0.0.1:<random-port> using a
// one-time cryptographic token, streaming contiguous bytes from the part file from the
// very first second so Chrome's native download shelf shows live progress with no copy at the end.
//
// Integrity guarantee: When TotalBytes > 0, the final byte is held until SHA-256 and size
// verification succeed. If verification fails, the TCP connection is aborted before the last
// byte is sent so Chrome never saves a corrupt file.
type LiveStreamServer struct {
	URL      string
	Done     <-chan bool
	listener net.Listener
	server   *http.Server
}

// ServeLiveStream starts a one-time loopback HTTP server for an active download.
func ServeLiveStream(info StartInfo, connectTimeout time.Duration) (*LiveStreamServer, error) {
	if info.PartPath == "" || info.ProgressState == nil {
		return nil, fmt.Errorf("invalid StartInfo for live stream")
	}
	fileName := info.FileName
	if fileName == "" {
		fileName = filepath.Base(info.PartPath)
	}
	if connectTimeout <= 0 {
		connectTimeout = 20 * time.Second
	}

	tokBytes := make([]byte, 24)
	if _, err := rand.Read(tokBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokBytes)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}

	expectedPath := "/handover/" + token + "/" + url.PathEscape(fileName)
	var claimed atomic.Bool
	doneCh := make(chan bool, 1)

	mux := http.NewServeMux()
	srv := &http.Server{
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 0, // streaming response may run for the duration of a large download
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath && r.URL.EscapedPath() != expectedPath {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/octet-stream")
			if info.TotalBytes > 0 {
				w.Header().Set("Content-Length", strconv.FormatInt(info.TotalBytes, 10))
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !claimed.CompareAndSwap(false, true) {
			http.Error(w, "token already used", http.StatusGone)
			return
		}

		f, err := os.Open(info.PartPath)
		if err != nil {
			http.Error(w, "part file missing", http.StatusInternalServerError)
			doneCh <- false
			go func() { _ = srv.Close() }()
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", "application/octet-stream")
		if info.TotalBytes > 0 {
			w.Header().Set("Content-Length", strconv.FormatInt(info.TotalBytes, 10))
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}

		buf := make([]byte, 64*1024)
		var sent int64
		for {
			contig, verifiedDone, dlErr := info.ProgressState()
			if dlErr != nil {
				// Abort the TCP connection immediately so Chrome sees a truncated stream and fails cleanly.
				if hj, ok := w.(http.Hijacker); ok {
					if conn, _, herr := hj.Hijack(); herr == nil {
						_ = conn.Close()
					}
				}
				doneCh <- false
				go func() { _ = srv.Close() }()
				return
			}

			// Hold back the final byte until integrity verification (SHA-256 + size) succeeds.
			limit := contig
			if !verifiedDone && info.TotalBytes > 0 && limit >= info.TotalBytes {
				limit = info.TotalBytes - 1
			}

			for sent < limit {
				toRead := int64(len(buf))
				if limit-sent < toRead {
					toRead = limit - sent
				}
				n, rerr := f.ReadAt(buf[:toRead], sent)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						doneCh <- false
						go func() { _ = srv.Close() }()
						return
					}
					sent += int64(n)
					if fl, ok := w.(http.Flusher); ok {
						fl.Flush()
					}
				}
				if rerr != nil {
					if rerr == io.EOF {
						break
					}
					doneCh <- false
					go func() { _ = srv.Close() }()
					return
				}
			}

			if verifiedDone && sent >= contig {
				if fl, ok := w.(http.Flusher); ok {
					fl.Flush()
				}
				_ = ln.Close()
				doneCh <- true
				return
			}

			select {
			case <-r.Context().Done():
				doneCh <- false
				go func() { _ = srv.Close() }()
				return
			case <-time.After(25 * time.Millisecond):
			}
		}
	})

	go func() {
		_ = srv.Serve(ln)
	}()

	go func() {
		time.Sleep(connectTimeout)
		if claimed.CompareAndSwap(false, true) {
			doneCh <- false
			_ = srv.Close()
		}
	}()

	return &LiveStreamServer{
		URL:      "http://" + ln.Addr().String() + expectedPath,
		Done:     doneCh,
		listener: ln,
		server:   srv,
	}, nil
}

// Close stops the live stream server immediately.
func (s *LiveStreamServer) Close() {
	if s != nil && s.server != nil {
		_ = s.server.Shutdown(context.Background())
	}
}

