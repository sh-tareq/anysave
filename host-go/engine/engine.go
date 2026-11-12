// Package engine implements AnySave's high-speed multi-connection HTTP(S) download
// engine with dynamic segmentation, adaptive connection scaling, crash-safe resume,
// ETag/Last-Modified/If-Range validation, automatic single-connection fallback,
// and SHA-256 integrity verification.
package engine

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
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
	DefaultUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
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
	ID      int           `json:"id"`
	Start   int64         `json:"start"`
	Current int64         `json:"current"`
	End     int64         `json:"end"`
	Status  SegmentStatus `json:"status"`
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
	RetryBaseDelay time.Duration
	OnStart        func(StartInfo)
	OnProgress     func(Progress)
	// OnVerified is called after SHA-256 and size verification succeed, while partPath is still intact.
	// If it returns streamed=true, the caller has already streamed the file out (e.g. via LiveStreamServer),
	// so the engine deletes the part file instead of moving it to DestDir.
	OnVerified     func() (streamed bool)
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
		opts.MaxRetries = 5
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
	opts           Options
	client         *http.Client
	cookieHeader   string
	partPath       string
	metaPath       string
	partFile       *os.File
	mu             sync.Mutex
	state          resumeState
	singleConnOnly bool
	etagChanged    atomic.Bool
	activeWorkers  atomic.Int32
	peakWorkers    atomic.Int32
	downloadedNow  atomic.Int64
	resumed        bool
	verifiedDone   bool
	verifiedSize   int64
	terminalErr    error
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
		Proxy:                 http.ProxyFromEnvironment,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   MaxAllowedConns + 8,
		MaxConnsPerHost:       MaxAllowedConns + 8,
		IdleConnTimeout:       60 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	client := &http.Client{
		Transport: tr,
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
	go d.progressAndScaleLoop(attemptCtx, progDone, cancelAttempt)

	var workerErr error
	var errOnce sync.Once
	setErr := func(err error) {
		if err != nil {
			errOnce.Do(func() {
				workerErr = err
				cancelAttempt()
			})
		}
	}

	if !d.state.AcceptRanges {
		// Single-stream download (either server doesn't support Range, or Content-Length is unknown).
		err := d.downloadSingleStream(attemptCtx, firstBody)
		if err != nil {
			setErr(err)
		}
	} else {
		var wg sync.WaitGroup

		// Start Worker #0 using the already-open firstBody (if not resumed).
		if firstBody != nil && firstSeg != nil {
			wg.Add(1)
			d.incWorkers()
			go func() {
				defer wg.Done()
				defer d.decWorkers()
				if err := d.runWorker(attemptCtx, firstSeg, firstBody); err != nil {
					setErr(err)
				}
			}()
		}

		// Spawn initial additional workers up to InitialConns.
		initialTarget := opts.InitialConns
		if firstBody != nil {
			initialTarget--
		}
		for i := 0; i < initialTarget; i++ {
			seg := d.claimOrSplitSegment()
			if seg == nil {
				break
			}
			wg.Add(1)
			d.incWorkers()
			go func(s *Segment) {
				defer wg.Done()
				defer d.decWorkers()
				if err := d.runWorker(attemptCtx, s, nil); err != nil {
					setErr(err)
				}
			}(seg)
		}

		// Wait for workers; if any segments remain pending (e.g. after single-conn downgrade
		// or adaptive scaling opportunity), keep running until all segments are SegDone or error.
		for {
			wg.Wait()
			if d.etagChanged.Load() {
				d.setTerminalErr(errETagChanged)
				close(progDone)
				_ = d.partFile.Close()
				return nil, errETagChanged
			}
			if workerErr != nil || attemptCtx.Err() != nil {
				break
			}
			seg := d.claimOrSplitSegment()
			if seg == nil {
				break
			}
			wg.Add(1)
			d.incWorkers()
			go func(s *Segment) {
				defer wg.Done()
				defer d.decWorkers()
				if err := d.runWorker(attemptCtx, s, nil); err != nil {
					setErr(err)
				}
			}(seg)
		}
	}

	close(progDone)

	if d.etagChanged.Load() {
		d.setTerminalErr(errETagChanged)
		_ = d.partFile.Close()
		return nil, errETagChanged
	}
	if ctx.Err() != nil {
		d.setTerminalErr(ctx.Err())
		d.mu.Lock()
		d.resetActiveToPendingLocked()
		d.saveStateLocked()
		d.mu.Unlock()
		return nil, ctx.Err()
	}
	if workerErr != nil {
		d.setTerminalErr(workerErr)
		d.mu.Lock()
		d.resetActiveToPendingLocked()
		d.saveStateLocked()
		d.mu.Unlock()
		return nil, workerErr
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
			MultiConn:  d.state.AcceptRanges && !d.singleConnOnly,
			PeakConns:  int(d.peakWorkers.Load()),
		}, nil
	}

	finalPath := uniqueDestPath(opts.DestDir, d.state.FileName)
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
	} else if d.singleConnOnly {
		fallbackReason = "server limited client to 1 connection"
	}

	return &Result{
		FilePath:       finalPath,
		FileName:       filepath.Base(finalPath),
		TotalBytes:     gotSize,
		SHA256:         gotHash,
		ETag:           d.state.ETag,
		Resumed:        d.resumed,
		MultiConn:      d.state.AcceptRanges && !d.singleConnOnly && d.peakWorkers.Load() > 1,
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

func (d *activeDownload) openFirstConnection(ctx context.Context) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= d.opts.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(d.opts.RetryBaseDelay * time.Duration(attempt)):
			}
		}
		req, err := d.newRequest(ctx, d.opts.URL)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Range", "bytes=0-")
		resp, err := d.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent {
			return resp, nil
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
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
// Active segment in half if its remaining bytes >= 2 * MinSplitSize.
func (d *activeDownload) claimOrSplitSegment() *Segment {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.singleConnOnly && d.activeWorkers.Load() >= 1 {
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

	if d.singleConnOnly {
		return nil
	}

	// 2. Find the Active segment with the largest remaining byte range.
	var best *Segment
	var bestRem int64
	for _, s := range d.state.Segments {
		if s.Status != SegActive {
			continue
		}
		rem := s.End - s.Current + 1
		if rem > bestRem {
			bestRem = rem
			best = s
		}
	}
	if best == nil || bestRem < 2*d.opts.MinSplitSize {
		return nil
	}

	mid := best.Current + bestRem/2
	oldEnd := best.End
	best.End = mid - 1

	newSeg := &Segment{
		ID:      len(d.state.Segments),
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
		next := d.claimOrSplitSegment()
		if next == nil {
			return nil
		}
		seg = next
	}
}

func (d *activeDownload) downloadSegment(ctx context.Context, seg *Segment, initialBody io.ReadCloser) error {
	buf := make([]byte, 32*1024)
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
				retries++
				if retries > d.opts.MaxRetries {
					return fmt.Errorf("segment %d failed after %d retries: %w", seg.ID, d.opts.MaxRetries, err)
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(d.opts.RetryBaseDelay * time.Duration(retries)):
				}
				continue
			}

			// Detect if the server refused a secondary connection (403 / 429).
			if (resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) &&
				(seg.ID > 0 || d.activeWorkers.Load() > 1) {
				resp.Body.Close()
				d.mu.Lock()
				d.singleConnOnly = true
				if seg.Current <= seg.End {
					seg.Status = SegPending
				} else {
					seg.Status = SegDone
				}
				d.saveStateLocked()
				d.mu.Unlock()
				return nil
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
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(d.opts.RetryBaseDelay * time.Duration(retries)):
				}
				continue
			}
			body = resp.Body
		}

		// Stream bytes for this segment until seg.Current > seg.End or error.
		var readErr error
		madeProgress := false
		for {
			n, err := body.Read(buf)
			if n > 0 {
				d.mu.Lock()
				rem := seg.End - seg.Current + 1
				if rem <= 0 {
					seg.Status = SegDone
					d.mu.Unlock()
					break
				}
				toWrite := int64(n)
				if toWrite > rem {
					toWrite = rem
				}
				writeOff := seg.Current
				seg.Current += toWrite
				doneSeg := seg.Current > seg.End
				if doneSeg {
					seg.Status = SegDone
				}
				d.mu.Unlock()

				if _, werr := d.partFile.WriteAt(buf[:toWrite], writeOff); werr != nil {
					_ = body.Close()
					return werr
				}
				d.downloadedNow.Add(toWrite)
				madeProgress = true
				if doneSeg {
					break
				}
			}
			if err != nil {
				readErr = err
				break
			}
		}
		_ = body.Close()

		d.mu.Lock()
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
		if madeProgress && retries > 0 {
			retries-- // reward forward progress while still bounding total failures
		}
		retries++
		if retries > d.opts.MaxRetries {
			if readErr == nil {
				readErr = io.ErrUnexpectedEOF
			}
			return fmt.Errorf("connection dropped before segment completed: %w", readErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d.opts.RetryBaseDelay * time.Duration(retries)):
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
			if d.state.AcceptRanges && !d.singleConnOnly &&
				int(d.activeWorkers.Load()) < d.opts.MaxConns &&
				speed > prevSpeed*1.08 {
				if seg := d.claimOrSplitSegment(); seg != nil {
					d.incWorkers()
					go func(s *Segment) {
						defer d.decWorkers()
						_ = d.runWorker(ctx, s, nil)
					}(seg)
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
