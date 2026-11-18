package engine

import (
	"bufio"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

// transferEncoding returns the compression the server applied to the response body
// ("" when the body is the file as-is).
func transferEncoding(resp *http.Response) string {
	enc := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Encoding")))
	switch enc {
	case "", "identity", "none":
		return ""
	case "x-gzip":
		return "gzip"
	}
	return enc
}

// canUnpack reports whether every listed encoding is one unpacker handles.
func canUnpack(encoding string) bool {
	for _, p := range strings.Split(encoding, ",") {
		switch strings.TrimSpace(p) {
		case "", "identity", "gzip", "x-gzip", "deflate", "br", "zstd":
		default:
			return false
		}
	}
	return true
}

type unpackedBody struct {
	io.Reader
	closers []io.Closer
	free    func()
}

func (u *unpackedBody) Close() error {
	if u.free != nil {
		u.free()
	}
	var first error
	for _, c := range u.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// unpacker wraps body so reads return the original file. Several encodings may be
// listed ("gzip, br"); they were applied in that order, so they are undone in reverse.
func unpacker(body io.ReadCloser, encoding string) (io.ReadCloser, error) {
	parts := strings.Split(encoding, ",")
	var r io.Reader = body
	u := &unpackedBody{closers: []io.Closer{body}}
	for i := len(parts) - 1; i >= 0; i-- {
		switch strings.TrimSpace(parts[i]) {
		case "", "identity":
		case "gzip", "x-gzip":
			g, err := gzip.NewReader(r)
			if err != nil {
				return nil, err
			}
			r = g
		case "deflate":
			// Officially zlib-wrapped, but some servers send raw deflate.
			br := bufio.NewReader(r)
			if head, err := br.Peek(2); err == nil && (uint16(head[0])<<8|uint16(head[1]))%31 == 0 && head[0]&0x0f == 8 {
				z, err := zlib.NewReader(br)
				if err != nil {
					return nil, err
				}
				r = z
			} else {
				r = flate.NewReader(br)
			}
		case "br":
			r = brotli.NewReader(r)
		case "zstd":
			z, err := zstd.NewReader(r)
			if err != nil {
				return nil, err
			}
			u.free = z.Close
			r = z
		default:
			return nil, fmt.Errorf("unknown compression %q", parts[i])
		}
	}
	u.Reader = r
	return u, nil
}
