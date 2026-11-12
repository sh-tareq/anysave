package torture

import (
	"bytes"
	"io"
	"net/http"
	"sync"
	"testing"
)

func TestTortureServerEndpoints(t *testing.T) {
	srv := NewServer(64 * 1024)
	defer srv.Close()

	// 1. /normal Range request
	req, _ := http.NewRequest("GET", srv.URL+"/normal", nil)
	req.Header.Set("Range", "bytes=100-199")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || !bytes.Equal(body, srv.Payload[100:200]) {
		t.Fatalf("/normal Range failed: status=%d len=%d", resp.StatusCode, len(body))
	}

	// 2. /no-range ignores Range
	req, _ = http.NewRequest("GET", srv.URL+"/no-range", nil)
	req.Header.Set("Range", "bytes=0-99")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) != len(srv.Payload) {
		t.Fatalf("/no-range unexpected status=%d len=%d", resp.StatusCode, len(body))
	}

	// 3. /drop-midway drops first 2 times, succeeds on 3rd
	srv.DropCount.Store(2)
	for i := 0; i < 2; i++ {
		resp, err = http.Get(srv.URL + "/drop-midway")
		if err == nil {
			b, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr == nil && len(b) == len(srv.Payload) {
				t.Fatalf("expected drop on attempt %d, got full payload", i+1)
			}
		}
	}
	resp, err = http.Get(srv.URL + "/drop-midway")
	if err != nil {
		t.Fatalf("3rd attempt after drops failed: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(body, srv.Payload) {
		t.Fatal("3rd attempt payload mismatch")
	}

	// 4. /changing-etag returns V1 then V2 (and 200 full body when If-Range has V1 ETag)
	srv.MutateAfter.Store(1)
	resp1, err := http.Head(srv.URL + "/changing-etag")
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.Header.Get("ETag") != srv.ETag {
		t.Fatalf("expected initial ETag %s, got %s", srv.ETag, resp1.Header.Get("ETag"))
	}
	req2, _ := http.NewRequest("GET", srv.URL+"/changing-etag", nil)
	req2.Header.Set("Range", "bytes=1024-2047")
	req2.Header.Set("If-Range", srv.ETag)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || !bytes.Equal(body2, srv.PayloadV2) {
		t.Fatalf("expected If-Range mismatch to return 200 with full PayloadV2, got status=%d len=%d", resp2.StatusCode, len(body2))
	}

	// 5. /redirect chain resolves to /normal
	resp, err = http.Get(srv.URL + "/redirect")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Equal(body, srv.Payload) {
		t.Fatal("/redirect payload mismatch")
	}

	// 6. /single-conn-only rejects parallel connections with 403
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := http.Get(srv.URL + "/single-conn-only")
			if err != nil {
				return
			}
			_, _ = io.Copy(io.Discard, r.Body)
			r.Body.Close()
			codes <- r.StatusCode
		}()
	}
	wg.Wait()
	close(codes)
	saw403 := false
	for c := range codes {
		if c == http.StatusForbidden {
			saw403 = true
		}
	}
	if !saw403 {
		t.Fatal("expected at least one 403 Forbidden from /single-conn-only under concurrent access")
	}
}

