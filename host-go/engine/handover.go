package engine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"
)

// HandoverServer serves a single downloaded file over 127.0.0.1:<random-port> using a
// one-time cryptographic token so Chrome can adopt the completed file into its native
// download shelf without re-downloading it from the internet.
type HandoverServer struct {
	URL      string
	Done     <-chan bool
	listener net.Listener
	server   *http.Server
}

// ServeOneTimeFile binds to 127.0.0.1:0 and serves filePath at most once at a random
// one-time URL. If deleteAfterServe is true, filePath is removed once Chrome finishes
// reading the response stream.
func ServeOneTimeFile(filePath, fileName string, timeout time.Duration, deleteAfterServe bool) (*HandoverServer, error) {
	st, err := os.Stat(filePath)
	if err != nil || st.IsDir() {
		return nil, fmt.Errorf("handover file not found: %s", filePath)
	}
	if fileName == "" {
		fileName = filepath.Base(filePath)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
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
		WriteTimeout: 5 * time.Minute,
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != expectedPath && r.URL.EscapedPath() != expectedPath {
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
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

		f, err := os.Open(filePath)
		if err != nil {
			http.Error(w, "file missing", http.StatusInternalServerError)
			doneCh <- false
			go func() { _ = srv.Close() }()
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, copyErr := f.WriteTo(w)
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
		f.Close()

		if copyErr == nil && deleteAfterServe {
			_ = os.Remove(filePath)
		}
		_ = ln.Close()
		doneCh <- (copyErr == nil)
	})

	go func() {
		_ = srv.Serve(ln)
	}()

	go func() {
		time.Sleep(timeout)
		if claimed.CompareAndSwap(false, true) {
			doneCh <- false
			_ = srv.Close()
		}
	}()

	return &HandoverServer{
		URL:      "http://" + ln.Addr().String() + expectedPath,
		Done:     doneCh,
		listener: ln,
		server:   srv,
	}, nil
}

// Close stops the handover server immediately.
func (h *HandoverServer) Close() {
	if h != nil && h.server != nil {
		_ = h.server.Shutdown(context.Background())
	}
}

