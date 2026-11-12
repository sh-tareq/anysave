// Package engine implements AnySave's high-speed multi-connection HTTP(S) download
// engine with dynamic segmentation, adaptive connection scaling, crash-safe resume,
// ETag/Last-Modified/If-Range validation, automatic single-connection fallback,
// and SHA-256 integrity verification.
package engine

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	DefaultInitialConns = 4
	DefaultMaxConns     = 16
	MaxAllowedConns     = 32
	DefaultMinSplitSize = 256 * 1024 // 256 KB minimum before splitting a segment
	// DefaultUserAgent says honestly who we are. Servers answer it like any download tool.
	DefaultUserAgent = "AnySave/1.1 (+https://github.com/sh-tareq/anysave)"
	// BrowserUserAgent is only tried when a server refuses the honest one with 403.
	BrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
)

var errETagChanged = errors.New("remote file changed mid-download (ETag mismatch)")

// SegmentStatus represents the state of a byte range.
type SegmentStatus string

const (
	SegPending SegmentStatus = "pending"
	SegActive  SegmentStatus = "active"
	SegDone    SegmentStatus = "done"
)

// Segment represents a contiguous byte range [Start, End] (inclusive).
type Segment struct {
	ID       int           `json:"id"`
	Start    int64         `json:"start"`
	Current  int64         `json:"current"`
	End      int64         `json:"end"`
	Status   SegmentStatus `json:"status"`
	inFlight int64
	// streamEnd is the last byte the HTTP response this piece is reading from will deliver
	// (-1 = no response open or being requested). It can be beyond End after a split.
	// streamPos is how far that response has been read. Both are guarded by activeDownload.mu.
	streamEnd int64
	streamPos int64
	probe     *atomic.Bool // set while the worker that owns this piece waits for its new connection's answer
}

// Progress reports live download telemetry to the caller.
type Progress struct {
	DoneBytes   int64
	TotalBytes  int64
	Percent     float64
	SpeedBPS    float64
	ETASeconds  int
	ActiveConns int
	Segments    []Segment
}

// Options configures a download job.
type Options struct {
	URL            string
	DestDir        string
	FileName       string // optional; discovered from headers/URL if empty
	StateKey       string // optional stable job ID for resume state files
	Cookies        string // optional Netscape cookies.txt or raw Cookie header
	Referrer       string
	UserAgent      string
	ExpectedSHA256 string // optional hex SHA-256 to verify on completion
	InitialConns   int
	MaxConns       int
	MinSplitSize   int64
	MaxRetries     int
	IdleTimeout    time.Duration // a connection that sends nothing for this long is dropped and retried (default 45s)
	RetryBaseDelay time.Duration
	MaxSpeedBPS    int64 // optional speed limit in bytes/sec (0 = unlimited)
	// Trace, when set, receives one line per HTTP request the engine makes (for diagnostics).
	Trace      func(string)
	OnStart    func(StartInfo)
	OnProgress func(Progress)
	// OnVerified is called after SHA-256 and size verification succeed, while partPath is still intact.
	// If it returns streamed=true, the caller has already streamed the file out (e.g. via LiveStreamServer),
	// so the engine deletes the part file instead of moving it to DestDir.
	OnVerified func() (streamed bool)
}

// Result describes a completed download.
type Result struct {
	FilePath       string
	FileName       string
	TotalBytes     int64
	SHA256         string
	ETag           string
	Resumed        bool
	MultiConn      bool
	PeakConns      int
	FallbackReason string // non-empty if server refused Range or multi-conn
}

type resumeState struct {
	URL          string     `json:"url"`
	ResolvedURL  string     `json:"resolvedUrl"`
	FileName     string     `json:"fileName"`
	TotalBytes   int64      `json:"totalBytes"`
	ETag         string     `json:"etag"`
	LastModified string     `json:"lastModified"`
	SHA256       string     `json:"sha256"`
	AcceptRanges bool       `json:"acceptRanges"`
	Segments     []*Segment `json:"segments"`
}

// ErrFallbackRecommended indicates the server requires browser session handling or
// rejected automated requests in a way that should fall back to Chrome's downloader.
type ErrFallbackRecommended struct {
	StatusCode int
	Reason     string
}

func (e *ErrFallbackRecommended) Error() string {
	return fmt.Sprintf("fallback to browser recommended (HTTP %d: %s)", e.StatusCode, e.Reason)
}

// Download executes a multi-connection download with automatic resume and integrity checks.
func Download(ctx context.Context, opts Options) (*Result, error) {
	if opts.URL == "" {
		return nil, errors.New("download URL is required")
	}
	if opts.DestDir == "" {
		return nil, errors.New("destination directory is required")
	}
	if err := os.MkdirAll(opts.DestDir, 0o755); err != nil {
		return nil, err
	}
	if opts.InitialConns <= 0 {
		opts.InitialConns = DefaultInitialConns
	}
	if opts.MaxConns <= 0 {
		opts.MaxConns = DefaultMaxConns
	}
	if opts.MaxConns > MaxAllowedConns {
		opts.MaxConns = MaxAllowedConns
	}
	if opts.InitialConns > opts.MaxConns {
		opts.InitialConns = opts.MaxConns
	}
	if opts.MinSplitSize <= 0 {
		opts.MinSplitSize = DefaultMinSplitSize
	}
	if opts.MaxRetries <= 0 {
		opts.MaxRetries = 8
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 45 * time.Second
	}
	if opts.RetryBaseDelay <= 0 {
		opts.RetryBaseDelay = 200 * time.Millisecond
	}
	if opts.UserAgent == "" {
		opts.UserAgent = DefaultUserAgent
	}

	// Up to 3 restarts if the remote server swaps out the file mid-download (ETag change).
	for attempt := 0; attempt < 3; attempt++ {
		res, err := runDownloadAttempt(ctx, opts, attempt > 0)
		if errors.Is(err, errETagChanged) {
			continue
		}
		return res, err
	}
	return nil, errors.New("remote file kept changing during download (ETag mismatch)")
}

type activeDownload struct {
	opts         Options
	client       *http.Client
	cookieHeader string
	partPath     string
	metaPath     string
	partFile     *os.File
	mu           sync.Mutex
	state        resumeState
	// connLimit is how many connections the server turned out to allow (0 = no limit seen yet).
	// It is learned when an extra connection is refused or can't be opened, and only goes down.
	connLimit  int
	workerExit chan struct{} // gets a value whenever a worker ends
	// opening counts new connections that were asked for but not answered yet. While it is
	// above zero no further connection is opened, so a server with a connection limit sees
	// at most one refused request instead of a burst (bursts get clients blocked).
	opening atomic.Int32
	// noSplitUntil: after a refusal, no new piece is split off before this time (guarded by mu).
	noSplitUntil   time.Time
	failOnce       sync.Once
	failErr        error
	cancelAttempt  context.CancelFunc
	etagChanged    atomic.Bool
	activeWorkers  atomic.Int32
	peakWorkers    atomic.Int32
	downloadedNow  atomic.Int64
	resumed        bool
	verifiedDone   bool
	verifiedSize   int64
	terminalErr    error
	rateMu         sync.Mutex
	rateTokens     float64
	rateLastRefill time.Time
}

func (d *activeDownload) waitRateLimit(ctx context.Context, n int) error {
	limit := d.opts.MaxSpeedBPS
	if limit <= 0 || n <= 0 {
		return nil
	}
	d.rateMu.Lock()
	now := time.Now()
	if d.rateLastRefill.IsZero() {
		d.rateLastRefill = now
		d.rateTokens = 0
	} else {
		elapsed := now.Sub(d.rateLastRefill).Seconds()
		if elapsed > 0 {
			d.rateTokens += elapsed * float64(limit)
			maxBurst := float64(limit) * 0.25
			if maxBurst < 16*1024 {
				maxBurst = 16 * 1024
			}
			if d.rateTokens > maxBurst {
				d.rateTokens = maxBurst
			}
			d.rateLastRefill = now
		}
	}
	d.rateTokens -= float64(n)
	var waitDur time.Duration
	if d.rateTokens < 0 {
		waitSec := (-d.rateTokens) / float64(limit)
		waitDur = time.Duration(waitSec * float64(time.Second))
	}
	d.rateMu.Unlock()

	if waitDur > 0 {
		timer := time.NewTimer(waitDur)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func (d *activeDownload) progressState() (int64, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.terminalErr != nil {
		return 0, false, d.terminalErr
	}
	if d.verifiedDone {
		return d.verifiedSize, true, nil
	}
	if !d.state.AcceptRanges {
		return d.downloadedNow.Load(), false, nil
	}
	var offset int64
	for {
		progressed := false
		for _, seg := range d.state.Segments {
			if seg.Start <= offset {
				if seg.Status == SegDone && seg.End+1 > offset {
					offset = seg.End + 1
					progressed = true
				} else if seg.Status != SegDone && seg.Current > offset {
					offset = seg.Current
				}
			}
		}
		if !progressed {
			break
		}
	}
	return offset, false, nil
}

func (d *activeDownload) setTerminalErr(err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.terminalErr == nil {
		d.terminalErr = err
	}
}

func runDownloadAttempt(ctx context.Context, opts Options, forceFresh bool) (*Result, error) {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		// A connection that can't be opened fails in seconds, not after the OS default (over a minute).
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   MaxAllowedConns + 8,
		MaxConnsPerHost:       MaxAllowedConns + 8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		// HTTP/1.1 on purpose: HTTP/2 would send every worker's request down one shared
		// connection, and the speed-up comes from each worker having its own.
		TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{},
	}
	var rt http.RoundTripper = tr
	if opts.Trace != nil {
		rt = &traceTransport{next: tr, log: opts.Trace, t0: time.Now()}
	}
	client := &http.Client{
		Transport: rt,
		Timeout:   0, // streaming downloads use per-request context cancellation
	}
	defer tr.CloseIdleConnections()

	key := opts.StateKey
	if key == "" {
		sum := sha256.Sum256([]byte(opts.URL + "|" + opts.FileName))
		key = hex.EncodeToString(sum[:8])
	}
	partsDir := filepath.Join(opts.DestDir, ".anysave-parts", key)
	if forceFresh {
		_ = os.RemoveAll(partsDir)
	}
	if err := os.MkdirAll(partsDir, 0o755); err != nil {
		return nil, err
	}

	d := &activeDownload{
		opts:         opts,
		client:       client,
		cookieHeader: buildCookieHeader(opts.URL, opts.Cookies),
		partPath:     filepath.Join(partsDir, "download.part"),
		metaPath:     filepath.Join(partsDir, "state.json"),
	}

	// Check if a valid resume state exists on disk.
	if !forceFresh {
		d.tryLoadResumeState()
	}

	attemptCtx, cancelAttempt := context.WithCancel(ctx)
	defer cancelAttempt()
	d.cancelAttempt = cancelAttempt
	d.workerExit = make(chan struct{}, 1)

	var firstBody io.ReadCloser
	var firstSeg *Segment

	if !d.resumed {
		// Open connection #0 with Range: bytes=0- to probe metadata AND immediately start streaming.
		resp, err := d.openFirstConnection(attemptCtx)
		if err != nil {
			return nil, err
		}
		firstBody = resp.Body

		// Determine filename
		fname := opts.FileName
		if fname == "" {
			fname = extractFileName(resp)
		}
		if fname == "" {
			fname = "download.bin"
		}
		fname = sanitizeFileName(fname)

		etag := strings.TrimSpace(resp.Header.Get("ETag"))
		lastMod := strings.TrimSpace(resp.Header.Get("Last-Modified"))
		remoteHash := strings.TrimSpace(resp.Header.Get("X-Content-SHA256"))
		if opts.ExpectedSHA256 != "" {
			remoteHash = opts.ExpectedSHA256
		}

		acceptRanges, totalBytes := parseRangeProbe(resp)

		d.state = resumeState{
			URL:          opts.URL,
			ResolvedURL:  resp.Request.URL.String(),
			FileName:     fname,
			TotalBytes:   totalBytes,
			ETag:         etag,
			LastModified: lastMod,
			SHA256:       remoteHash,
			AcceptRanges: acceptRanges && totalBytes > 0,
		}

		f, err := os.OpenFile(d.partPath, os.O_CREATE|os.O_RDWR|os.O_TRUNC, 0o600)
		if err != nil {
			firstBody.Close()
			return nil, err
		}
		d.partFile = f
		if totalBytes > 0 {
			_ = f.Truncate(totalBytes)
		}

		if d.state.AcceptRanges {
			firstSeg = &Segment{
				ID:      0,
				Start:   0,
				Current: 0,
				End:     totalBytes - 1,
				Status:  SegActive,
			}
			d.state.Segments = []*Segment{firstSeg}
			d.saveStateLocked()
		}
	} else {
		f, err := os.OpenFile(d.partPath, os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		d.partFile = f
	}

	defer func() {
		if d.partFile != nil {
			_ = d.partFile.Close()
		}
	}()

	if opts.OnStart != nil {
		opts.OnStart(StartInfo{
			FileName:      d.state.FileName,
			TotalBytes:    d.state.TotalBytes,
			AcceptRanges:  d.state.AcceptRanges,
			PartPath:      d.partPath,
			ProgressState: d.progressState,
		})
	}

	// Progress reporter & adaptive connection scaler
	progDone := make(chan struct{})
	scaleExited := make(chan struct{})
	go func() {
		defer close(scaleExited)
		d.progressAndScaleLoop(attemptCtx, progDone, cancelAttempt)
	}()

	if !d.state.AcceptRanges {
		// Single-stream download (either server doesn't support Range, or Content-Length is unknown).
		if err := d.downloadSingleStream(attemptCtx, firstBody); err != nil {
			d.fail(err)
		}
	} else {
		// Start Worker #0 using the already-open firstBody (if not resumed).
		if firstBody != nil && firstSeg != nil {
			d.spawn(attemptCtx, firstSeg, firstBody)
		}

		// Spawn initial additional workers up to InitialConns.
		initialTarget := opts.InitialConns
		if firstBody != nil {
			initialTarget--
		}
		for i := 0; i < initialTarget; i++ {
			seg := d.claimOrSplitSegment(false)
			if seg == nil {
				break
			}
			d.spawn(attemptCtx, seg, nil)
		}

		// Run until every worker has ended. Whenever none is left but pieces remain (a worker
		// handed its piece back because the server refused the extra connection), start one
		// more. Every worker is counted, including the ones the scaler adds, so the file is
		// never checked while something is still writing to it.
		for {
			if d.activeWorkers.Load() > 0 {
				<-d.workerExit
				continue
			}
			if d.failErr != nil || attemptCtx.Err() != nil || d.etagChanged.Load() {
				break
			}
			seg := d.claimOrSplitSegment(false)
			if seg == nil {
				break
			}
			d.spawn(attemptCtx, seg, nil)
		}
	}

	// Stop the scaler, then wait for any worker it started in its last tick.
	close(progDone)
	<-scaleExited
	for d.activeWorkers.Load() > 0 {
		<-d.workerExit
	}

	keepForResume := func() {
		d.mu.Lock()
		d.resetActiveToPendingLocked()
		d.saveStateLocked()
		d.mu.Unlock()
	}
	if d.etagChanged.Load() {
		d.setTerminalErr(errETagChanged)
		_ = d.partFile.Close()
		return nil, errETagChanged
	}
	if ctx.Err() != nil {
		d.setTerminalErr(ctx.Err())
		keepForResume()
		return nil, ctx.Err()
	}
	if d.failErr != nil {
		d.setTerminalErr(d.failErr)
		keepForResume()
		return nil, d.failErr
	}
	// The part file is created at its full size, so its size says nothing about whether
	// every piece arrived. Refuse to hand over a file with a hole in it.
	if d.state.AcceptRanges {
		d.mu.Lock()
		var missing int64
		for _, s := range d.state.Segments {
			if s.Current <= s.End {
				missing += s.End - s.Current + 1
			}
		}
		d.mu.Unlock()
		if missing > 0 {
			err := fmt.Errorf("download incomplete: %d bytes missing", missing)
			d.setTerminalErr(err)
			keepForResume()
			return nil, err
		}
	}

	// Sync and close partFile before integrity verification and final move.
	_ = d.partFile.Sync()
	_ = d.partFile.Close()
	d.partFile = nil

	// Verify exact size and SHA-256 hash.
	gotHash, gotSize, err := verifyFileIntegrity(d.partPath, d.state.TotalBytes, d.state.SHA256)
	if err != nil {
		d.setTerminalErr(err)
		_ = os.RemoveAll(partsDir)
		return nil, err
	}

	d.mu.Lock()
	d.verifiedDone = true
	d.verifiedSize = gotSize
	d.mu.Unlock()

	if opts.OnVerified != nil && opts.OnVerified() {
		finalPath := filepath.Join(opts.DestDir, d.state.FileName)
		_ = os.RemoveAll(partsDir)
		if opts.OnProgress != nil {
			opts.OnProgress(Progress{
				DoneBytes:   gotSize,
				TotalBytes:  gotSize,
				Percent:     100,
				SpeedBPS:    0,
				ETASeconds:  0,
				ActiveConns: 0,
			})
		}
		return &Result{
			FilePath:   finalPath,
			FileName:   d.state.FileName,
			TotalBytes: gotSize,
			SHA256:     gotHash,
			ETag:       d.state.ETag,
			Resumed:    d.resumed,
			MultiConn:  d.state.AcceptRanges && d.peakWorkers.Load() > 1,
			PeakConns:  int(d.peakWorkers.Load()),
		}, nil
	}

	finalPath := uniqueDestPath(opts.DestDir, d.state.FileName)
	// The part file is private while downloading; the finished file gets normal permissions,
	// like any other download.
	_ = os.Chmod(d.partPath, 0o644)
	if err := os.Rename(d.partPath, finalPath); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(partsDir)

	// Emit final 100% progress.
	if opts.OnProgress != nil {
		opts.OnProgress(Progress{
			DoneBytes:   gotSize,
			TotalBytes:  gotSize,
			Percent:     100,
			SpeedBPS:    0,
			ETASeconds:  0,
			ActiveConns: 0,
		})
	}

	fallbackReason := ""
	if !d.state.AcceptRanges {
		fallbackReason = "server does not support byte ranges"
	} else if d.connLimit == 1 {
		fallbackReason = "server limited client to 1 connection"
	}

	return &Result{
		FilePath:       finalPath,
		FileName:       filepath.Base(finalPath),
		TotalBytes:     gotSize,
		SHA256:         gotHash,
		ETag:           d.state.ETag,
		Resumed:        d.resumed,
		MultiConn:      d.state.AcceptRanges && d.peakWorkers.Load() > 1,
		PeakConns:      int(d.peakWorkers.Load()),
		FallbackReason: fallbackReason,
	}, nil
}

func (d *activeDownload) incWorkers() {
	cur := d.activeWorkers.Add(1)
	for {
		prev := d.peakWorkers.Load()
		if cur <= prev || d.peakWorkers.CompareAndSwap(prev, cur) {
			break
		}
	}
}

func (d *activeDownload) decWorkers() {
	d.activeWorkers.Add(-1)
}

func (d *activeDownload) tryLoadResumeState() {
	raw, err := os.ReadFile(d.metaPath)
	if err != nil {
		return
	}
	var st resumeState
	if err := json.Unmarshal(raw, &st); err != nil || !st.AcceptRanges || st.TotalBytes <= 0 || len(st.Segments) == 0 {
		return
	}
	fi, err := os.Stat(d.partPath)
	if err != nil || fi.Size() != st.TotalBytes {
		return
	}
	for _, s := range st.Segments {
		if s.Status == SegActive {
			s.Status = SegPending
		}
		if s.Current > s.End {
			s.Status = SegDone
		}
	}
	if d.opts.URL != "" && d.opts.URL != st.URL {
		// Support refreshing an expired signed link while keeping all downloaded segments intact.
		st.URL = d.opts.URL
		st.ResolvedURL = ""
	}
	d.state = st
	d.resumed = true
}

func (d *activeDownload) resetActiveToPendingLocked() {
	for _, s := range d.state.Segments {
		if s.Status == SegActive {
			if s.Current > s.End {
				s.Status = SegDone
			} else {
				s.Status = SegPending
			}
		}
	}
}

func (d *activeDownload) saveStateLocked() {
	if !d.state.AcceptRanges {
		return
	}
	b, err := json.Marshal(d.state)
	if err != nil {
		return
	}
	tmp := d.metaPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err == nil {
		_ = os.Rename(tmp, d.metaPath)
	}
}

// openFirstConnection opens connection #0. Servers differ in which client names they accept:
// some refuse unknown tools with 403, others hang up on anything that claims to be a browser
// but doesn't behave like one. So it tries the given name first, then the others, and keeps
// whichever one the server answered (d.opts.UserAgent) for the rest of the download.
func (d *activeDownload) openFirstConnection(ctx context.Context) (*http.Response, error) {
	agents := []string{d.opts.UserAgent}
	for _, ua := range []string{DefaultUserAgent, BrowserUserAgent} {
		if ua != d.opts.UserAgent {
			agents = append(agents, ua)
		}
	}
	uaIdx, uaFails := 0, 0
	nextAgent := func() bool {
		if uaIdx+1 >= len(agents) {
			return false
		}
		uaIdx++
		uaFails = 0
		d.opts.UserAgent = agents[uaIdx]
		return true
	}

	var lastErr error
	for attempt := 0; attempt <= d.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := d.backoff(ctx, attempt, ""); err != nil {
				return nil, err
			}
		}
		req, err := d.newRequest(ctx, d.opts.URL)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", "bytes=0-")
		resp, err := d.client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			lastErr = err
			// Hung up on twice in a row under this name: try the next one.
			if uaFails++; uaFails >= 2 {
				nextAgent()
			}
			continue
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
			return resp, nil
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
			if resp.StatusCode == http.StatusForbidden && nextAgent() {
				attempt--
				continue
			}
			return nil, &ErrFallbackRecommended{
				StatusCode: resp.StatusCode,
				Reason:     fmt.Sprintf("server returned HTTP %d", resp.StatusCode),
			}
		}
		if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		return nil, fmt.Errorf("download failed with HTTP %d", resp.StatusCode)
	}
	return nil, fmt.Errorf("failed to open connection: %w", lastErr)
}

func (d *activeDownload) newRequest(ctx context.Context, targetURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", d.opts.UserAgent)
	if d.cookieHeader != "" {
		req.Header.Set("Cookie", d.cookieHeader)
	}
	if d.opts.Referrer != "" {
		req.Header.Set("Referer", d.opts.Referrer)
	}
	return req, nil
}

// claimOrSplitSegment either claims an unassigned Pending segment or splits the largest
// Active segment in half if its remaining bytes >= 2 * MinSplitSize. It returns nil when
// there is nothing to take, or when the server's connection limit is already in use.
// self is true when a running worker asks for its next piece (it doesn't count against
// the limit, since it is about to reuse its own slot).
func (d *activeDownload) claimOrSplitSegment(self bool) *Segment {
	d.mu.Lock()
	defer d.mu.Unlock()

	others := int(d.activeWorkers.Load())
	if self {
		others--
	} else if d.opening.Load() > 0 {
		return nil
	}
	if d.connLimit > 0 && others >= d.connLimit {
		return nil
	}

	// 1. Check for any Pending segment first.
	for _, s := range d.state.Segments {
		if s.Status == SegPending {
			if s.Current > s.End {
				s.Status = SegDone
				continue
			}
			s.Status = SegActive
			d.saveStateLocked()
			return s
		}
	}

	// A refused request means the server wants fewer of them: don't ask again too soon.
	if time.Now().Before(d.noSplitUntil) {
		return nil
	}

	// 2. Find the Active segment with the largest remaining byte range.
	var best *Segment
	var bestRem int64
	var bestPos int64
	for _, s := range d.state.Segments {
		if s.Status != SegActive {
			continue
		}
		pos := s.Current
		if s.inFlight > pos {
			pos = s.inFlight
		}
		rem := s.End - pos + 1
		if rem > bestRem {
			bestRem = rem
			best = s
			bestPos = pos
		}
	}
	if best == nil || bestRem < 2*d.opts.MinSplitSize {
		return nil
	}

	mid := bestPos + bestRem/2
	oldEnd := best.End
	best.End = mid - 1

	nextID := 0
	for _, s := range d.state.Segments {
		if s.ID >= nextID {
			nextID = s.ID + 1
		}
	}
	newSeg := &Segment{
		ID:      nextID,
		Start:   mid,
		Current: mid,
		End:     oldEnd,
		Status:  SegActive,
	}
	d.state.Segments = append(d.state.Segments, newSeg)
	d.saveStateLocked()
	return newSeg
}

func (d *activeDownload) runWorker(ctx context.Context, seg *Segment, initialBody io.ReadCloser) error {
	for {
		if err := d.downloadSegment(ctx, seg, initialBody); err != nil {
			return err
		}
		initialBody = nil

		// Immediately claim or split the next largest segment!
		next := d.claimOrSplitSegment(true)
		if next == nil {
			return nil
		}
		seg = next
	}
}

// spawn starts a counted worker on seg. Its error, if any, fails the whole attempt.
func (d *activeDownload) spawn(ctx context.Context, seg *Segment, body io.ReadCloser) {
	d.incWorkers()
	var probe *atomic.Bool
	if body == nil {
		probe = &atomic.Bool{}
		probe.Store(true)
		d.opening.Add(1)
	}
	seg.probe = probe
	go func() {
		defer func() {
			d.probeAnswered(ctx, probe, false)
			d.decWorkers()
			select {
			case d.workerExit <- struct{}{}:
			default:
			}
		}()
		if err := d.runWorker(ctx, seg, body); err != nil {
			d.fail(err)
		}
	}()
}

// probeAnswered is called when a newly opened connection got its answer (or its worker ended
// without one). If the server accepted it and we are still below the starting number of
// connections, the next one is opened: a polite ramp-up, one at a time.
func (d *activeDownload) probeAnswered(ctx context.Context, probe *atomic.Bool, accepted bool) {
	if probe == nil || !probe.CompareAndSwap(true, false) {
		return
	}
	d.opening.Add(-1)
	if !accepted || ctx.Err() != nil || int(d.activeWorkers.Load()) >= d.opts.InitialConns {
		return
	}
	if next := d.claimOrSplitSegment(false); next != nil {
		d.spawn(ctx, next, nil)
	}
}

// fail records the first error and stops the other workers.
func (d *activeDownload) fail(err error) {
	if err == nil {
		return
	}
	d.failOnce.Do(func() {
		d.failErr = err
		if d.cancelAttempt != nil {
			d.cancelAttempt()
		}
	})
}

// yieldToOthers is called when a worker's connection was refused or couldn't be opened.
// If other workers are still downloading, the server (or network) evidently allows fewer
// connections than we tried: remember that limit, give the piece back, and let the worker
// end. It returns false when this is the only worker, which must then retry itself.
func (d *activeDownload) yieldToOthers(seg *Segment, retryAfter string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	wait := 3 * time.Second
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 3 {
		wait = time.Duration(min(secs, 30)) * time.Second
	}
	if until := time.Now().Add(wait); until.After(d.noSplitUntil) {
		d.noSplitUntil = until
	}
	n := int(d.activeWorkers.Load())
	if n <= 1 {
		return false
	}
	if d.connLimit == 0 || n-1 < d.connLimit {
		d.connLimit = n - 1
	}
	seg.streamEnd = -1
	if d.mergeBackLocked(seg) {
		d.saveStateLocked()
		return true
	}
	if seg.Current <= seg.End {
		seg.Status = SegPending
	} else {
		seg.Status = SegDone
	}
	seg.inFlight = seg.Current
	d.saveStateLocked()
	return true
}

// mergeBackLocked gives an untouched piece back to the piece just before it, when that one
// is still being downloaded and its open response already covers these bytes (it was split
// off from it) or it has no response open yet. The bytes then arrive without a new request,
// which matters on servers that limit how many requests a visitor may make.
func (d *activeDownload) mergeBackLocked(seg *Segment) bool {
	if seg.Current != seg.Start || seg.Current > seg.End {
		return false
	}
	for _, prev := range d.state.Segments {
		if prev == seg || prev.Status != SegActive || prev.End+1 != seg.Start || prev.Current > prev.End {
			continue
		}
		// Not if its response stops short of the piece, or was already read past its own end
		// (those extra bytes were thrown away).
		if prev.streamEnd != -1 && (prev.streamEnd < seg.End || prev.streamPos > prev.End+1) {
			return false
		}
		prev.End = seg.End
		for i, s := range d.state.Segments {
			if s == seg {
				d.state.Segments = append(d.state.Segments[:i], d.state.Segments[i+1:]...)
				break
			}
		}
		seg.Status = SegDone
		seg.End = seg.Start - 1 // nothing left for the worker that held it
		return true
	}
	return false
}

// backoff waits before retry number n: the base delay doubling each time, capped at 100x
// the base (20s by default), or what the server asked for in Retry-After (up to 30s).
func (d *activeDownload) backoff(ctx context.Context, n int, retryAfter string) error {
	delay := d.opts.RetryBaseDelay
	for i := 1; i < n && delay < 100*d.opts.RetryBaseDelay; i++ {
		delay *= 2
	}
	if delay > 100*d.opts.RetryBaseDelay {
		delay = 100 * d.opts.RetryBaseDelay
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil && secs > 0 {
		if ra := time.Duration(secs) * time.Second; ra > delay {
			delay = ra
		}
		if delay > 30*time.Second {
			delay = 30 * time.Second
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(delay):
		return nil
	}
}

func (d *activeDownload) downloadSegment(ctx context.Context, seg *Segment, initialBody io.ReadCloser) error {
	bufSize := 32 * 1024
	if d.opts.MaxSpeedBPS > 0 && d.opts.MaxSpeedBPS < 512*1024 {
		bufSize = 8 * 1024
	}
	buf := make([]byte, bufSize)
	retries := 0

	for {
		if ctx.Err() != nil {
			if initialBody != nil {
				initialBody.Close()
			}
			return ctx.Err()
		}

		d.mu.Lock()
		start := seg.Current
		end := seg.End
		if start > end {
			seg.Status = SegDone
			d.saveStateLocked()
			d.mu.Unlock()
			if initialBody != nil {
				initialBody.Close()
			}
			return nil
		}
		etag := d.state.ETag
		lastMod := d.state.LastModified
		targetURL := d.state.ResolvedURL
		if targetURL == "" {
			targetURL = d.state.URL
		}
		seg.streamPos = start
		seg.streamEnd = end
		if initialBody != nil {
			seg.streamEnd = d.state.TotalBytes - 1 // the first request was open-ended
		}
		d.mu.Unlock()

		var body io.ReadCloser
		if initialBody != nil {
			body = initialBody
			initialBody = nil
		} else {
			req, err := d.newRequest(ctx, targetURL)
			if err != nil {
				return err
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			if etag != "" && !strings.HasPrefix(etag, "W/") {
				req.Header.Set("If-Range", etag)
			} else if lastMod != "" {
				req.Header.Set("If-Range", lastMod)
			}

			resp, err := d.client.Do(req)
			if err != nil {
				d.probeAnswered(ctx, seg.probe, false)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// Couldn't connect. If other workers are getting data, this extra connection
				// isn't wanted: hand the piece back rather than fail the download.
				if d.yieldToOthers(seg, "") {
					return nil
				}
				retries++
				if retries > d.opts.MaxRetries {
					return fmt.Errorf("segment %d failed after %d retries: %w", seg.ID, d.opts.MaxRetries, err)
				}
				if berr := d.backoff(ctx, retries, ""); berr != nil {
					return berr
				}
				continue
			}

			// The server refused this connection (too many at once, or it is busy).
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests ||
				resp.StatusCode == http.StatusServiceUnavailable {
				retryAfter := resp.Header.Get("Retry-After")
				resp.Body.Close()
				d.probeAnswered(ctx, seg.probe, false)
				if d.yieldToOthers(seg, retryAfter) {
					return nil
				}
				// The only connection left: wait and try again.
				retries++
				if retries > d.opts.MaxRetries {
					return fmt.Errorf("unexpected HTTP status %d on range request", resp.StatusCode)
				}
				if berr := d.backoff(ctx, retries, retryAfter); berr != nil {
					return berr
				}
				continue
			}

			// Detect mid-download file change via ETag or 200 OK in response to If-Range.
			respETag := strings.TrimSpace(resp.Header.Get("ETag"))
			if (etag != "" && respETag != "" && respETag != etag) || resp.StatusCode == http.StatusOK {
				resp.Body.Close()
				d.etagChanged.Store(true)
				return errETagChanged
			}

			if resp.StatusCode != http.StatusPartialContent {
				resp.Body.Close()
				retries++
				if retries > d.opts.MaxRetries {
					return fmt.Errorf("unexpected HTTP status %d on range request", resp.StatusCode)
				}
				if berr := d.backoff(ctx, retries, ""); berr != nil {
					return berr
				}
				continue
			}
			d.probeAnswered(ctx, seg.probe, true)
			body = resp.Body
		}

		// A connection that goes silent (Wi-Fi dropped, laptop slept) would block Read
		// forever. Closing the body from the timer wakes it up with an error, and the
		// piece is retried from where it stopped.
		idle := time.AfterFunc(d.opts.IdleTimeout, func() { _ = body.Close() })

		// Stream bytes for this segment until seg.Current > seg.End or error.
		var readErr error
		madeProgress := false
		for {
			n, err := body.Read(buf)
			if n > 0 {
				idle.Reset(d.opts.IdleTimeout)
				d.mu.Lock()
				rem := seg.End - seg.Current + 1
				if rem <= 0 {
					seg.Status = SegDone
					seg.inFlight = seg.Current
					d.mu.Unlock()
					break
				}
				toWrite := int64(n)
				if toWrite > rem {
					toWrite = rem
				}
				writeOff := seg.Current
				seg.inFlight = writeOff + toWrite
				seg.streamPos = writeOff + int64(n)
				d.mu.Unlock()

				if _, werr := d.partFile.WriteAt(buf[:toWrite], writeOff); werr != nil {
					idle.Stop()
					_ = body.Close()
					return werr
				}

				d.mu.Lock()
				seg.Current = writeOff + toWrite
				doneSeg := seg.Current > seg.End
				if doneSeg {
					seg.Status = SegDone
				}
				d.mu.Unlock()

				d.downloadedNow.Add(toWrite)
				madeProgress = true
				if rerr := d.waitRateLimit(ctx, int(toWrite)); rerr != nil {
					idle.Stop()
					_ = body.Close()
					return rerr
				}
				idle.Reset(d.opts.IdleTimeout) // time spent waiting for the speed limit isn't the server's
				if doneSeg {
					break
				}
			}
			if err != nil {
				readErr = err
				break
			}
		}
		idle.Stop()
		_ = body.Close()

		d.mu.Lock()
		seg.streamEnd = -1
		done := seg.Current > seg.End
		if done {
			seg.Status = SegDone
		}
		d.saveStateLocked()
		d.mu.Unlock()

		if done {
			return nil
		}

		// Segment not yet finished (e.g. mid-stream TCP drop / unexpected EOF).
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if madeProgress {
			retries = 0 // the connection worked for a while: only count failures in a row
		}
		retries++
		if retries > d.opts.MaxRetries {
			if readErr == nil {
				readErr = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("connection dropped before segment completed: %w", readErr)
		}
		if berr := d.backoff(ctx, retries, ""); berr != nil {
			return berr
		}
	}
}

func (d *activeDownload) downloadSingleStream(ctx context.Context, firstBody io.ReadCloser) error {
	buf := make([]byte, 32*1024)
	retries := 0
	body := firstBody

	for {
		if body == nil {
			req, err := d.newRequest(ctx, d.state.URL)
			if err != nil {
				return err
			}
			resp, err := d.client.Do(req)
			if err != nil {
				retries++
				if retries > d.opts.MaxRetries {
					return err
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(d.opts.RetryBaseDelay * time.Duration(retries)):
				}
				continue
			}
			if resp.StatusCode != http.StatusOK {
				resp.Body.Close()
				return fmt.Errorf("HTTP %d", resp.StatusCode)
			}
			body = resp.Body
		}

		if _, err := d.partFile.Seek(0, io.SeekStart); err != nil {
			body.Close()
			return err
		}
		_ = d.partFile.Truncate(0)
		d.downloadedNow.Store(0)

		var written int64
		var readErr error
		for {
			if ctx.Err() != nil {
				body.Close()
				return ctx.Err()
			}
			n, err := body.Read(buf)
			if n > 0 {
				if _, werr := d.partFile.Write(buf[:n]); werr != nil {
					body.Close()
					return werr
				}
				written += int64(n)
				d.downloadedNow.Store(written)
				if rerr := d.waitRateLimit(ctx, n); rerr != nil {
					body.Close()
					return rerr
				}
			}
			if err != nil {
				readErr = err
				break
			}
		}
		body.Close()
		body = nil

		if readErr == io.EOF && (d.state.TotalBytes <= 0 || written == d.state.TotalBytes) {
			return nil
		}

		retries++
		if retries > d.opts.MaxRetries {
			if d.state.TotalBytes > 0 && written != d.state.TotalBytes {
				return fmt.Errorf("unexpected EOF: received %d of %d bytes", written, d.state.TotalBytes)
			}
			return readErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.opts.RetryBaseDelay * time.Duration(retries)):
		}
	}
}

func (d *activeDownload) progressAndScaleLoop(ctx context.Context, done <-chan struct{}, _ context.CancelFunc) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	lastBytes := d.downloadedNow.Load()
	lastTime := time.Now()
	var prevSpeed float64

	if d.opts.OnProgress != nil {
		d.mu.Lock()
		tot := d.state.TotalBytes
		d.mu.Unlock()
		d.opts.OnProgress(Progress{
			DoneBytes:   lastBytes,
			TotalBytes:  tot,
			Percent:     0,
			SpeedBPS:    0,
			ETASeconds:  -1,
			ActiveConns: int(d.activeWorkers.Load()),
		})
	}

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			dt := now.Sub(lastTime).Seconds()
			if dt <= 0 {
				continue
			}
			curBytes := d.downloadedNow.Load()
			speed := float64(curBytes-lastBytes) / dt
			lastBytes = curBytes
			lastTime = now

			d.mu.Lock()
			total := d.state.TotalBytes
			var doneBytes int64
			var snap []Segment
			if d.state.AcceptRanges {
				snap = make([]Segment, len(d.state.Segments))
				for i, s := range d.state.Segments {
					snap[i] = *s
					doneSeg := s.Current - s.Start
					if doneSeg > 0 {
						doneBytes += doneSeg
					}
				}
			} else {
				doneBytes = curBytes
			}
			d.saveStateLocked()
			d.mu.Unlock()

			pct := 0.0
			eta := -1
			if total > 0 {
				pct = float64(doneBytes) / float64(total) * 100
				if pct > 100 {
					pct = 100
				}
				if speed > 0 && total > doneBytes {
					eta = int(float64(total-doneBytes) / speed)
				}
			}

			if d.opts.OnProgress != nil {
				d.opts.OnProgress(Progress{
					DoneBytes:   doneBytes,
					TotalBytes:  total,
					Percent:     pct,
					SpeedBPS:    speed,
					ETASeconds:  eta,
					ActiveConns: int(d.activeWorkers.Load()),
					Segments:    snap,
				})
			}

			// Adaptive connection scaling: if throughput grew vs previous interval and we are
			// below MaxConns, spawn an extra worker if a segment is large enough to split.
			// claimOrSplitSegment refuses once the server's connection limit is known and in use.
			if d.state.AcceptRanges && ctx.Err() == nil &&
				int(d.activeWorkers.Load()) < d.opts.MaxConns &&
				speed > prevSpeed*1.08 {
				if seg := d.claimOrSplitSegment(false); seg != nil {
					d.spawn(ctx, seg, nil)
				}
			}
			if speed > 0 {
				prevSpeed = speed
			}
		}
	}
}

func parseRangeProbe(resp *http.Response) (acceptRanges bool, totalBytes int64) {
	if resp.StatusCode == http.StatusPartialContent {
		cr := resp.Header.Get("Content-Range")
		// e.g. "bytes 0-999/1000"
		if idx := strings.LastIndexByte(cr, '/'); idx >= 0 {
			if tot, err := strconv.ParseInt(strings.TrimSpace(cr[idx+1:]), 10, 64); err == nil && tot > 0 {
				return true, tot
			}
		}
		if resp.ContentLength > 0 {
			return true, resp.ContentLength
		}
	}
	return false, resp.ContentLength
}

func verifyFileIntegrity(path string, expectedSize int64, expectedSHA256 string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if expectedSize > 0 && st.Size() != expectedSize {
		return "", st.Size(), fmt.Errorf("file size mismatch: got %d bytes, expected %d", st.Size(), expectedSize)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", st.Size(), err
	}
	gotHash := hex.EncodeToString(h.Sum(nil))
	if expectedSHA256 != "" {
		want := strings.ToLower(strings.TrimSpace(expectedSHA256))
		if gotHash != want {
			return gotHash, st.Size(), fmt.Errorf("file checksum mismatch: got SHA-256 %s, expected %s", gotHash, want)
		}
	}
	return gotHash, st.Size(), nil
}

func extractFileName(resp *http.Response) string {
	if disp := resp.Header.Get("Content-Disposition"); disp != "" {
		if _, params, err := mime.ParseMediaType(disp); err == nil {
			if fn := params["filename*"]; fn != "" {
				if idx := strings.Index(fn, "''"); idx >= 0 {
					if u, err := url.PathUnescape(fn[idx+2:]); err == nil && u != "" {
						return u
					}
				}
			}
			if fn := params["filename"]; fn != "" {
				return fn
			}
		}
	}
	if resp.Request != nil && resp.Request.URL != nil {
		base, _ := url.PathUnescape(filepath.Base(resp.Request.URL.Path))
		if base != "" && base != "/" && base != "." {
			return base
		}
	}
	return ""
}

func sanitizeFileName(s string) string {
	s = strings.Map(func(r rune) rune {
		if strings.ContainsRune(`\/:*?"<>|`+"\n\r\t", r) {
			return ' '
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 140 {
		ext := filepath.Ext(s)
		if len(ext) <= 10 {
			s = s[:140-len(ext)] + ext
		} else {
			s = s[:140]
		}
	}
	if s == "" || s == "." {
		return "download.bin"
	}
	return s
}

func uniqueDestPath(dir, fileName string) string {
	ext := filepath.Ext(fileName)
	base := strings.TrimSuffix(fileName, ext)
	p := filepath.Join(dir, fileName)
	for i := 1; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
	}
}

// buildCookieHeader parses either a Netscape cookies.txt string or a raw Cookie header
// and returns the matching "name=value; ..." header for targetURL.
func buildCookieHeader(targetURL, cookiesInput string) string {
	cookiesInput = strings.TrimSpace(cookiesInput)
	if cookiesInput == "" {
		return ""
	}
	if !strings.Contains(cookiesInput, "\t") && !strings.HasPrefix(cookiesInput, "#") {
		return cookiesInput
	}
	u, err := url.Parse(targetURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	isHTTPS := u.Scheme == "https"
	var pairs []string
	for _, line := range strings.Split(cookiesInput, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 7 {
			continue
		}
		dom := strings.TrimPrefix(strings.ToLower(fields[0]), ".")
		secure := strings.EqualFold(fields[3], "TRUE")
		name, val := fields[5], fields[6]
		if secure && !isHTTPS {
			continue
		}
		if host == dom || strings.HasSuffix(host, "."+dom) {
			pairs = append(pairs, name+"="+val)
		}
	}
	return strings.Join(pairs, "; ")
}

// traceTransport reports every request and its outcome to Options.Trace.
type traceTransport struct {
	next http.RoundTripper
	log  func(string)
	t0   time.Time
}

func (t *traceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	at := start.Sub(t.t0).Seconds()
	took := time.Since(start).Seconds()
	if err != nil {
		t.log(fmt.Sprintf("+%.2fs GET range=%q -> error after %.2fs: %v", at, req.Header.Get("Range"), took, err))
		return nil, err
	}
	t.log(fmt.Sprintf("+%.2fs GET range=%q -> %d in %.2fs (retry-after=%q)", at, req.Header.Get("Range"), resp.StatusCode, took, resp.Header.Get("Retry-After")))
	return resp, nil
}
