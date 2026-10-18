package app

// A brand logo URL is useless if the wizard cannot SERVE the bytes. handleIndex embeds
// only index.html, so /assets/<file> fell through to the index handler and the <img>
// pointed at an HTML page. This pins that a brand dir supplied by the caller is served.

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrandAssetsAreServed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("\x89PNG\r\n\x1a\nFAKE"), 0o644); err != nil {
		t.Fatal(err)
	}
	prev := brandAssetDir
	t.Cleanup(func() { brandAssetDir = prev })
	brandAssetDir = dir

	rec := httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/assets/logo.png", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/") {
		t.Errorf("Content-Type = %q, want an image type (the file fell through to the HTML handler)", ct)
	}
	if rec.Body.Len() < 8 || !strings.HasPrefix(rec.Body.String(), "\x89PNG") {
		t.Errorf("served %d bytes that are not the asset", rec.Body.Len())
	}
}

func TestBrandAssetPathTraversalIsRefused(t *testing.T) {
	dir := t.TempDir()
	prev := brandAssetDir
	t.Cleanup(func() { brandAssetDir = prev })
	brandAssetDir = dir

	rec := httptest.NewRecorder()
	handleIndex(rec, httptest.NewRequest("GET", "/assets/../../etc/passwd", nil))
	if strings.Contains(rec.Body.String(), "root:") {
		t.Fatalf("path traversal succeeded")
	}
}
