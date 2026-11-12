package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"anysave-host/torture"
)

func baseTestOpts(url, dir string) Options {
	return Options{
		URL:            url,
		DestDir:        dir,
		InitialConns:   4,
		MaxConns:       8,
		MinSplitSize:   16 * 1024, // 16 KB so 256 KB test files split across many segments
		MaxRetries:     4,
		RetryBaseDelay: 15 * time.Millisecond,
	}
}

func assertPayloadMatch(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read downloaded file %s: %v", path, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("downloaded file content mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestEngineTortureSuite(t *testing.T) {
	srv := torture.NewServer(256 * 1024) // 256 KB
	defer http.DefaultClient.CloseIdleConnections()
	defer srv.Close()

	t.Run("normal_multiconn", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/normal", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("normal download failed: %v", err)
		}
		if !res.MultiConn || res.PeakConns < 2 {
			t.Errorf("expected multi-connection download, got MultiConn=%v PeakConns=%d", res.MultiConn, res.PeakConns)
		}
		if res.SHA256 != srv.SHA256 {
			t.Errorf("SHA256 mismatch: got %s, want %s", res.SHA256, srv.SHA256)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("no_range_fallback", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/no-range", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("no-range download failed: %v", err)
		}
		if res.MultiConn {
			t.Errorf("expected single-stream fallback when Range is unsupported")
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("drop_midway_recovery", func(t *testing.T) {
		srv.DropCount.Store(3)
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/drop-midway", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("drop-midway download failed: %v", err)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("changing_etag_restarts_cleanly", func(t *testing.T) {
		srv.MutateAfter.Store(1)
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/changing-etag", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("changing-etag download failed: %v", err)
		}
		if res.SHA256 != srv.SHA256V2 {
			t.Fatalf("expected SHA256V2 %s after ETag mutation, got %s", srv.SHA256V2, res.SHA256)
		}
		assertPayloadMatch(t, res.FilePath, srv.PayloadV2)
	})

	t.Run("bad_length_short_rejected", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/bad-length-short", dir)
		opts.MaxRetries = 2
		res, err := Download(context.Background(), opts)
		if err == nil {
			t.Fatalf("expected error on truncated Content-Length response, got result %+v", res)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				t.Fatalf("corrupt file %s was left in destination dir", e.Name())
			}
		}
	})

	t.Run("no_length_chunked", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/no-length", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("no-length download failed: %v", err)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("redirect_chain", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/redirect", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("redirect download failed: %v", err)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("single_conn_only_403_downgrade", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/single-conn-only", dir)
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("single-conn-only download failed: %v", err)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	})

	t.Run("corrupt_hash_rejected", func(t *testing.T) {
		dir := t.TempDir()
		opts := baseTestOpts(srv.URL+"/corrupt-hash", dir)
		res, err := Download(context.Background(), opts)
		if err == nil {
			t.Fatalf("expected checksum mismatch error on /corrupt-hash, got %+v", res)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !e.IsDir() {
				t.Fatalf("corrupt file %s was left in destination dir", e.Name())
			}
		}
	})

	t.Run("pause_and_crash_safe_resume", func(t *testing.T) {
		slowSrv := torture.NewServer(512 * 1024) // 512 KB
		defer http.DefaultClient.CloseIdleConnections()
		defer slowSrv.Close()

		dir := t.TempDir()
		opts := baseTestOpts(slowSrv.URL+"/slow", dir)
		opts.StateKey = "resume-job-1"
		opts.InitialConns = 2
		opts.MaxConns = 2

		ctx1, cancel1 := context.WithTimeout(context.Background(), 35*time.Millisecond)
		defer cancel1()
		_, err := Download(ctx1, opts)
		if err == nil {
			t.Fatal("expected first download to be cancelled mid-flight by timeout")
		}

		// Verify state.json was persisted on pause/cancel.
		stateFile := filepath.Join(dir, ".anysave-parts", "resume-job-1", "state.json")
		if _, err := os.Stat(stateFile); err != nil {
			t.Fatalf("expected resume state file at %s: %v", stateFile, err)
		}

		// Now resume to completion.
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("resumed download failed: %v", err)
		}
		if !res.Resumed {
			t.Errorf("expected res.Resumed == true")
		}
		assertPayloadMatch(t, res.FilePath, slowSrv.Payload)
	})
}

func TestHandoverOneTimeServer(t *testing.T) {
	http.DefaultClient.CloseIdleConnections()
	dir := t.TempDir()
	fpath := filepath.Join(dir, "sample.bin")
	payload := []byte("verified handover payload bytes 1234567890")
	if err := os.WriteFile(fpath, payload, 0o600); err != nil {
		t.Fatal(err)
	}

	h, err := ServeOneTimeFile(fpath, "sample.bin", 5*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()

	// Wrong token path -> 404
	badResp, err := http.Get(h.URL + "-wrong")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(badResp.Body)
	badResp.Body.Close()
	if badResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 on wrong token path, got %d", badResp.StatusCode)
	}

	// First valid GET -> 200 OK with exact bytes
	resp, err := http.Get(h.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(got, payload) {
		t.Fatalf("handover GET failed: status=%d len=%d", resp.StatusCode, len(got))
	}

	ok := <-h.Done
	if !ok {
		t.Fatal("expected h.Done to report true")
	}

	// Second GET on same token -> 410 Gone or connection refused (server closed)
	resp2, err := http.Get(h.URL)
	if err == nil {
		resp2.Body.Close()
		if resp2.StatusCode == http.StatusOK {
			t.Fatal("expected one-time token to reject second download")
		}
	}
}

func TestSpeedBenchmarkSingleVsEngine(t *testing.T) {
	// 2 MB payload on throttled /slow server (~3.2 MB/s per connection)
	srv := torture.NewServer(2 * 1024 * 1024)
	defer srv.Close()

	// 1. Single connection
	dir1 := t.TempDir()
	t0 := time.Now()
	res1, err := Download(context.Background(), Options{
		URL:          srv.URL + "/slow",
		DestDir:      dir1,
		InitialConns: 1,
		MaxConns:     1,
		MinSplitSize: 2 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	dur1 := time.Since(t0)

	// 2. Multi-connection (8 connections)
	dir8 := t.TempDir()
	t1 := time.Now()
	res8, err := Download(context.Background(), Options{
		URL:          srv.URL + "/slow",
		DestDir:      dir8,
		InitialConns: 8,
		MaxConns:     16,
		MinSplitSize: 64 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	dur8 := time.Since(t1)

	mb := float64(len(srv.Payload)) / (1024 * 1024)
	speed1 := mb / dur1.Seconds()
	speed8 := mb / dur8.Seconds()
	t.Logf("Speed test (2 MB throttled server): 1-conn = %v (%.2f MB/s) vs Engine (%d conns) = %v (%.2f MB/s, %.1fx faster)",
		dur1.Round(time.Millisecond), speed1, res8.PeakConns, dur8.Round(time.Millisecond), speed8, speed8/speed1)

	assertPayloadMatch(t, res1.FilePath, srv.Payload)
	assertPayloadMatch(t, res8.FilePath, srv.Payload)
	if dur8 >= dur1 {
		t.Errorf("expected multi-connection engine (%v) to be faster than 1 connection (%v)", dur8, dur1)
	}
}

func TestLiveStreamHandover(t *testing.T) {
	srv := torture.NewServer(1024 * 1024) // 1 MB
	defer srv.Close()

	dir := t.TempDir()
	var live *LiveStreamServer
	streamedBytes := make(chan []byte, 1)
	streamErr := make(chan error, 1)

	res, err := Download(context.Background(), Options{
		URL:          srv.URL + "/slow",
		DestDir:      dir,
		FileName:     "live-streamed.bin",
		InitialConns: 4,
		MaxConns:     8,
		MinSplitSize: 64 * 1024,
		OnStart: func(info StartInfo) {
			var sErr error
			live, sErr = ServeLiveStream(info, 5*time.Second)
			if sErr != nil {
				streamErr <- sErr
				return
			}
			// Simulate Chrome immediately requesting the stream URL from the first second.
			go func(u string) {
				resp, gErr := http.Get(u)
				if gErr != nil {
					streamErr <- gErr
					return
				}
				defer resp.Body.Close()
				b, rErr := io.ReadAll(resp.Body)
				if rErr != nil {
					streamErr <- rErr
					return
				}
				streamedBytes <- b
			}(live.URL)
		},
		OnVerified: func() bool {
			if live == nil {
				return false
			}
			ok := <-live.Done
			live.Close()
			return ok
		},
	})
	if err != nil {
		t.Fatalf("live stream download failed: %v", err)
	}

	select {
	case sErr := <-streamErr:
		t.Fatalf("live stream client error: %v", sErr)
	case got := <-streamedBytes:
		if !bytes.Equal(got, srv.Payload) {
			t.Fatalf("live stream payload mismatch: got %d bytes", len(got))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for live stream client")
	}

	// Because OnVerified returned true, the engine should NOT leave a duplicate file in DestDir.
	if _, statErr := os.Stat(res.FilePath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no duplicate file at %s when streamed via LiveStreamServer", res.FilePath)
	}
}

func TestLiveStreamHandoverAbortsOnCorruptHash(t *testing.T) {
	srv := torture.NewServer(512 * 1024)
	defer srv.Close()

	dir := t.TempDir()
	var live *LiveStreamServer
	clientDone := make(chan int, 1)

	_, err := Download(context.Background(), Options{
		URL:          srv.URL + "/corrupt-hash",
		DestDir:      dir,
		FileName:     "corrupt-live.bin",
		InitialConns: 4,
		MinSplitSize: 64 * 1024,
		OnStart: func(info StartInfo) {
			var sErr error
			live, sErr = ServeLiveStream(info, 5*time.Second)
			if sErr != nil {
				return
			}
			go func(u string) {
				resp, gErr := http.Get(u)
				if gErr != nil {
					clientDone <- 0
					return
				}
				defer resp.Body.Close()
				b, _ := io.ReadAll(resp.Body)
				clientDone <- len(b)
			}(live.URL)
		},
	})
	if err == nil {
		t.Fatal("expected Download to fail on corrupt hash")
	}
	if live != nil {
		defer live.Close()
	}

	gotLen := <-clientDone
	if gotLen >= len(srv.Payload) {
		t.Fatalf("expected LiveStreamServer to hold back final byte and abort on corrupt hash, but client received %d / %d bytes", gotLen, len(srv.Payload))
	}
}

func TestSpeedLimiter(t *testing.T) {
	srv := torture.NewServer(512 * 1024) // 512 KB
	defer srv.Close()
	defer http.DefaultClient.CloseIdleConnections()

	dir := t.TempDir()
	// Limit to 1 MB/s (1024*1024 B/s) -> 512 KB should take ~500ms (>= 350ms).
	t0 := time.Now()
	res, err := Download(context.Background(), Options{
		URL:          srv.URL + "/normal",
		DestDir:      dir,
		InitialConns: 4,
		MaxConns:     8,
		MinSplitSize: 64 * 1024,
		MaxSpeedBPS:  1024 * 1024,
	})
	if err != nil {
		t.Fatalf("speed-limited download failed: %v", err)
	}
	dur := time.Since(t0)
	if dur < 350*time.Millisecond {
		t.Fatalf("expected speed limiter (1 MB/s on 512 KB) to take >= 350ms, took %v", dur)
	}
	assertPayloadMatch(t, res.FilePath, srv.Payload)
}

func TestRefreshExpiredLinkResume(t *testing.T) {
	srv := torture.NewServer(1024 * 1024) // 1 MB
	defer srv.Close()
	defer http.DefaultClient.CloseIdleConnections()

	dir := t.TempDir()
	ctx1, cancel1 := context.WithCancel(context.Background())

	// 1. Start downloading from "/normal?sig=expired-1" with a 512 KB/s speed limit
	// and cancel on the first progress tick once >= 64 KB is saved to disk.
	_, err := Download(ctx1, Options{
		URL:          srv.URL + "/normal?sig=expired-1",
		DestDir:      dir,
		FileName:     "refreshed.bin",
		StateKey:     "refresh-job-1",
		InitialConns: 4,
		MaxConns:     4,
		MinSplitSize: 64 * 1024,
		MaxSpeedBPS:  512 * 1024,
		OnProgress: func(p Progress) {
			if p.DoneBytes >= 64*1024 {
				cancel1()
			}
		},
	})
	if err == nil {
		t.Fatal("expected first download to be cancelled")
	}

	// 2. Resume with the SAME StateKey ("refresh-job-1") but a NEW URL ("/normal?sig=fresh-2").
	res, err := Download(context.Background(), Options{
		URL:          srv.URL + "/normal?sig=fresh-2",
		DestDir:      dir,
		FileName:     "refreshed.bin",
		StateKey:     "refresh-job-1",
		InitialConns: 4,
		MaxConns:     8,
		MinSplitSize: 64 * 1024,
	})
	if err != nil {
		t.Fatalf("resume with refreshed link failed: %v", err)
	}
	if !res.Resumed {
		t.Fatal("expected res.Resumed == true when continuing with refreshed link")
	}
	assertPayloadMatch(t, res.FilePath, srv.Payload)
}

// A server that allows only 3 connections at once: the engine must finish the file,
// keep using the 3 it is allowed, and never fail because the 4th was refused.
func TestConnectionLimitedServer(t *testing.T) {
	srv := torture.NewServer(2 * 1024 * 1024)
	defer srv.Close()
	opts := baseTestOpts(srv.URL+"/max-3-conns", t.TempDir())
	opts.InitialConns = 6
	opts.MaxConns = 12
	res, err := Download(context.Background(), opts)
	if err != nil {
		t.Fatalf("download from a 3-connection server failed: %v", err)
	}
	assertPayloadMatch(t, res.FilePath, srv.Payload)
	if got := srv.LimitedMax.Load(); got < 2 || got > 3 {
		t.Errorf("expected the engine to keep 2-3 connections busy, server saw at most %d", got)
	}
	if !res.MultiConn {
		t.Errorf("expected a multi-connection download, got PeakConns=%d", res.PeakConns)
	}
	// Connections are opened one at a time, so finding the limit costs one refused
	// request, not a burst (bursts of refused requests get clients blocked).
	if got := srv.LimitedRefused.Load(); got > 2 {
		t.Errorf("server had to refuse %d requests; expected at most 2", got)
	}
}

// A server with a request budget: 3 requests are served, later ones must wait 30 seconds.
// Pieces whose request is refused must go back to the connection they were split from,
// so the file still arrives quickly and whole.
func TestRequestLimitedServer(t *testing.T) {
	srv := torture.NewServer(1024 * 1024)
	defer srv.Close()
	opts := baseTestOpts(srv.URL+"/max-3-requests", t.TempDir())
	opts.MinSplitSize = 128 * 1024
	start := time.Now()
	res, err := Download(context.Background(), opts)
	if err != nil {
		t.Fatalf("download from a request-limited server failed: %v", err)
	}
	assertPayloadMatch(t, res.FilePath, srv.Payload)
	if took := time.Since(start); took > 15*time.Second {
		t.Errorf("refused pieces waited for the server's penalty: took %v", took)
	}
}

// Servers disagree about client names: one refuses download tools, another hangs up on
// pretend browsers. The engine must get the file from both.
func TestClientNameFallback(t *testing.T) {
	srv := torture.NewServer(256 * 1024)
	defer srv.Close()
	for _, tc := range []struct{ path, ua string }{
		{"/browsers-only", ""},
		{"/no-fake-browsers", BrowserUserAgent},
	} {
		opts := baseTestOpts(srv.URL+tc.path, t.TempDir())
		opts.UserAgent = tc.ua
		res, err := Download(context.Background(), opts)
		if err != nil {
			t.Fatalf("%s: download failed: %v", tc.path, err)
		}
		assertPayloadMatch(t, res.FilePath, srv.Payload)
	}
}

// Connections that go silent after a few bytes: the idle timeout must drop them and the
// pieces must be retried from where they stopped.
func TestStalledConnectionsRecover(t *testing.T) {
	srv := torture.NewServer(512 * 1024)
	defer srv.Close()
	srv.StallCount.Store(3)
	opts := baseTestOpts(srv.URL+"/stall", t.TempDir())
	opts.IdleTimeout = 300 * time.Millisecond
	start := time.Now()
	res, err := Download(context.Background(), opts)
	if err != nil {
		t.Fatalf("download with stalled connections failed: %v", err)
	}
	assertPayloadMatch(t, res.FilePath, srv.Payload)
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("stalled connections were not dropped quickly: took %v", took)
	}
}
