package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zerone-agent/agent-deployer/internal/model"
)

// serveBody starts a server returning body with the given status.
func serveBody(t *testing.T, status int, body []byte) (*httptest.Server, *int) {
	t.Helper()
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func validSource(name, url, fileName string, body []byte) (model.ToolSource, []byte) {
	return model.ToolSource{
		Name:     name,
		URL:      url,
		Hash:     hashOf(body),
		FileName: fileName,
	}, body
}

func TestInstaller_Install_AllExtensions(t *testing.T) {
	for _, ext := range []string{".ts", ".mts", ".js", ".mjs"} {
		t.Run(ext, func(t *testing.T) {
			body := []byte("export default { name: \"X\", description: \"d\" }")
			srv, _ := serveBody(t, http.StatusOK, body)
			src, _ := validSource("GetWeather", srv.URL, "whatever"+ext, body)

			dir := t.TempDir()
			inst := NewInstaller(nil, DefaultLimits())
			rel, err := inst.Install(context.Background(), src, dir)
			if err != nil {
				t.Fatalf("install %s: %v", ext, err)
			}
			if rel != "tools/GetWeather"+ext {
				t.Fatalf("rel = %q, want tools/GetWeather%s", rel, ext)
			}
			got, err := os.ReadFile(filepath.Join(dir, "GetWeather"+ext))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(body) {
				t.Fatalf("content mismatch")
			}
		})
	}
}

func TestInstaller_Install_5MiBBoundary(t *testing.T) {
	exact := bytesRepeat(5*1024*1024, 'x')
	srv, _ := serveBody(t, http.StatusOK, exact)
	src, _ := validSource("Big", srv.URL, "big.js", exact)

	inst := NewInstaller(nil, DefaultLimits())
	if _, err := inst.Install(context.Background(), src, t.TempDir()); err != nil {
		t.Fatalf("exactly 5 MiB must be accepted: %v", err)
	}
}

func TestInstaller_Install_OversizedRejected(t *testing.T) {
	over := bytesRepeat(5*1024*1024+1, 'x')
	srv, _ := serveBody(t, http.StatusOK, over)
	src, _ := validSource("Big", srv.URL, "big.js", over)

	inst := NewInstaller(nil, DefaultLimits())
	_, err := inst.Install(context.Background(), src, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("err = %v, want size-limit error", err)
	}
}

func TestInstaller_Install_EmptyRejected(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, nil)
	src := model.ToolSource{
		Name:     "Empty",
		URL:      srv.URL,
		Hash:     hashOf(nil),
		FileName: "e.js",
	}
	inst := NewInstaller(nil, DefaultLimits())
	_, err := inst.Install(context.Background(), src, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want empty-file error", err)
	}
}

func TestInstaller_Install_Non2xxRejected(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusMovedPermanently} {
		srv, _ := serveBody(t, status, []byte("x"))
		src := model.ToolSource{
			Name: "Bad", URL: srv.URL, Hash: strings.Repeat("a", 64), FileName: "bad.js",
		}
		inst := NewInstaller(nil, DefaultLimits())
		_, err := inst.Install(context.Background(), src, t.TempDir())
		if err == nil {
			t.Fatalf("status %d must be rejected", status)
		}
	}
}

func TestInstaller_Install_HashMismatch(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, []byte("content"))
	src := model.ToolSource{
		Name: "Bad", URL: srv.URL, Hash: strings.Repeat("a", 64), FileName: "bad.js",
	}
	inst := NewInstaller(nil, DefaultLimits())
	dir := t.TempDir()
	_, err := inst.Install(context.Background(), src, dir)
	if err == nil || !strings.Contains(err.Error(), ErrHashMismatch.Error()) {
		t.Fatalf("err = %v, want hash mismatch", err)
	}
	// No partial file left behind.
	if _, statErr := os.Stat(filepath.Join(dir, "Bad.js")); statErr == nil {
		t.Fatal("hash mismatch must not leave the artifact in place")
	}
}

func TestInstaller_Install_CacheHitSkipsDownload(t *testing.T) {
	body := []byte("v1")
	srv, hits := serveBody(t, http.StatusOK, body)
	src, _ := validSource("Cached", srv.URL, "c.js", body)

	inst := NewInstaller(nil, DefaultLimits())
	dir := t.TempDir()
	if _, err := inst.Install(context.Background(), src, dir); err != nil {
		t.Fatal(err)
	}
	if *hits != 1 {
		t.Fatalf("hits = %d after first install", *hits)
	}
	if _, err := inst.Install(context.Background(), src, dir); err != nil {
		t.Fatal(err)
	}
	if *hits != 1 {
		t.Fatalf("cache hit must not re-download: hits = %d", *hits)
	}
}

func TestInstaller_Install_ChangedHashReplacesArtifact(t *testing.T) {
	v1 := []byte("v1")
	v2 := []byte("v2-content-changed")
	// The server always serves v2. v1 is pre-seeded on disk (artifact +
	// matching marker) to simulate a previously-installed artifact: running
	// Install(src1) against a v2-serving server would correctly fail the
	// hash check (brief Step 4 note).
	srv, hits := serveBody(t, http.StatusOK, v2)
	src2, _ := validSource("Evolve", srv.URL, "e.js", v2)

	inst := NewInstaller(nil, DefaultLimits())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Evolve.js"), v1, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Evolve.js.sha256"), []byte(hashOf(v1)), 0644); err != nil {
		t.Fatal(err)
	}

	// src2 declares v2's hash: the v1 marker no longer matches, so the
	// installer must re-download and replace the artifact in place.
	if _, err := inst.Install(context.Background(), src2, dir); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Evolve.js"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(v2) {
		t.Fatalf("artifact not replaced: %q", got)
	}
	// Re-install v2: cache hit (single source of truth is the marker).
	before := *hits
	if _, err := inst.Install(context.Background(), src2, dir); err != nil {
		t.Fatal(err)
	}
	if *hits != before {
		t.Fatal("re-install of identical artifact must be a cache hit")
	}
}

func TestInstaller_Install_CacheMissWhenArtifactDeleted(t *testing.T) {
	body := []byte("v1")
	srv, hits := serveBody(t, http.StatusOK, body)
	src, _ := validSource("Phantom", srv.URL, "p.js", body)

	inst := NewInstaller(nil, DefaultLimits())
	dir := t.TempDir()
	if _, err := inst.Install(context.Background(), src, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "Phantom.js")); err != nil {
		t.Fatal(err)
	}
	// Marker exists but artifact is gone: must re-download, not fake-hit.
	if _, err := inst.Install(context.Background(), src, dir); err != nil {
		t.Fatal(err)
	}
	if *hits != 2 {
		t.Fatalf("hits = %d, want 2 (marker alone must not be a cache hit)", *hits)
	}
}

func TestInstaller_Install_CleansTempOnFailure(t *testing.T) {
	srv, _ := serveBody(t, http.StatusOK, []byte("content"))
	src := model.ToolSource{
		Name: "Tmp", URL: srv.URL, Hash: strings.Repeat("a", 64), FileName: "t.js",
	}
	inst := NewInstaller(nil, DefaultLimits())
	dir := t.TempDir()
	_, err := inst.Install(context.Background(), src, dir)
	if err == nil {
		t.Fatal("expected hash mismatch")
	}
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("temp files left behind: %v", names)
	}
}

func TestInstaller_Install_InvalidSourceRejectedWithoutDownload(t *testing.T) {
	srv, hits := serveBody(t, http.StatusOK, []byte("x"))
	src := model.ToolSource{Name: "Bad", URL: srv.URL, Hash: "zz", FileName: "x.py"}
	inst := NewInstaller(nil, DefaultLimits())
	if _, err := inst.Install(context.Background(), src, t.TempDir()); err == nil {
		t.Fatal("invalid source must fail")
	}
	if *hits != 0 {
		t.Fatal("invalid source must not trigger a download")
	}
}

// bytesRepeat is a tiny helper to keep the 5 MiB test allocations explicit.
func bytesRepeat(n int, b byte) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
