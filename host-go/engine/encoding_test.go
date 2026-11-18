package engine

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/andybalholm/brotli"
)

// A server that packs the file in transit even though it was asked not to, and claims
// to accept byte ranges (of the packed data), like the export link that saved a CSV as gzip.
func packedServer(t *testing.T, plain []byte, enc string) *httptest.Server {
	var packed bytes.Buffer
	switch enc {
	case "gzip":
		w := gzip.NewWriter(&packed)
		w.Write(plain)
		w.Close()
	case "br":
		w := brotli.NewWriter(&packed)
		w.Write(plain)
		w.Close()
	default:
		packed.WriteString("???")
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Encoding", enc)
		w.Header().Set("Content-Disposition", `attachment; filename="export.csv"`)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(packed.Bytes()))
	}))
}

func TestPackedInTransit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("id,name,revenue\n")
	for i := 0; i < 20000; i++ {
		fmt.Fprintf(&sb, "%d,row %d,%d.50\n", i, i, i*3)
	}
	plain := []byte(sb.String())
	for _, enc := range []string{"gzip", "br"} {
		t.Run(enc, func(t *testing.T) {
			srv := packedServer(t, plain, enc)
			defer srv.Close()
			res, err := Download(context.Background(), baseTestOpts(srv.URL+"/x", t.TempDir()))
			if err != nil {
				t.Fatal(err)
			}
			assertPayloadMatch(t, res.FilePath, plain)
		})
	}
	t.Run("unknown packing goes to the browser", func(t *testing.T) {
		srv := packedServer(t, plain, "weird")
		defer srv.Close()
		_, err := Download(context.Background(), baseTestOpts(srv.URL+"/x", t.TempDir()))
		if _, ok := err.(*ErrFallbackRecommended); !ok {
			t.Fatalf("want fallback, got %v", err)
		}
	})
}
