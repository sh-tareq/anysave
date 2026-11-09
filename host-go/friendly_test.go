package main

import (
	"strings"
	"testing"
)

func TestFriendlyError(t *testing.T) {
	cases := map[string]string{
		`[generic] Got HTTP Error 403 caused by Cloudflare anti-bot challenge; try again with --extractor-args "generic:impersonate"`: "blocks automated downloads",
		"[generic] Unsupported URL: https://example.com/": "No downloadable video",
		"This video is DRM protected": "DRM-protected",
		"HTTP Error 404: Not Found": "wasn't found (404)",
		"Some other error": "Some other error",
	}
	for in, want := range cases {
		if got := friendlyError(in); !strings.Contains(got, want) {
			t.Errorf("friendlyError(%q) = %q, want it to contain %q", in, got, want)
		}
	}
}
