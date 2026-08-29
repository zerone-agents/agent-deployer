package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

func TestInstaller_DownloadErrors_DoNotLeakURL(t *testing.T) {
	// Shut the server down so client.Do fails at the transport level with a
	// *url.Error that would normally embed the full URL + query string.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	urlWithSecret := srv.URL + "/t.js?token=SECRET"
	srv.Close()

	src := model.ToolSource{
		Name:     "Leaky",
		URL:      urlWithSecret,
		Hash:     strings.Repeat("a", 64),
		FileName: "l.js",
	}
	inst := NewInstaller(nil, DefaultLimits())
	_, err := inst.Install(context.Background(), src, t.TempDir())
	if err == nil {
		t.Fatal("expected download failure against a closed server")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), urlWithSecret) {
		t.Fatalf("error leaks URL details: %v", err)
	}
	if !strings.Contains(err.Error(), "tool download failed") {
		t.Fatalf("error should still be wrapped in ErrDownloadFailed: %v", err)
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

func TestInstaller_DownloadParseError_DoesNotLeakURL(t *testing.T) {
	// White-box: download() does not pre-validate, so a malformed URL reaches
	// http.NewRequestWithContext, whose parse error is a *url.Error embedding
	// the raw URL. The strip must keep only the reason.
	inst := NewInstaller(nil, DefaultLimits())
	_, err := inst.download(context.Background(), "http://ex ample.com/t.js?token=SECRET", filepath.Join(t.TempDir(), "out"))
	if err == nil {
		t.Fatal("expected parse failure for malformed URL")
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "ex ample.com") {
		t.Fatalf("error leaks URL details: %v", err)
	}
}

func TestInstaller_Install_RedirectTo2xxRejected(t *testing.T) {
	body := []byte("export default { name: 'Redirected' }")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect) // 307 with Location: real redirect
		case "/final":
			_, _ = w.Write(body)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	sum := sha256.Sum256(body)
	src := model.ToolSource{
		Name:     "Redirected",
		URL:      srv.URL + "/redirect",
		Hash:     hex.EncodeToString(sum[:]),
		FileName: "r.mjs",
	}
	inst := NewInstaller(nil, DefaultLimits()) // nil → default client, still must not follow
	dir := t.TempDir()
	_, err := inst.Install(context.Background(), src, dir)
	if err == nil {
		t.Fatal("redirect chain must be rejected even when the final response is 2xx")
	}
	if !strings.Contains(err.Error(), "http status 307") {
		t.Fatalf("error should report the rejected 3xx status: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "Redirected.mjs")); statErr == nil {
		t.Fatal("no artifact may be left behind by a rejected redirect")
	}
}

func TestInstaller_DownloadLocalStorageError_NotUpstreamFailure(t *testing.T) {
	// os.Create fails with EISDIR when dest is an existing directory —
	// deterministic local-storage failure, no network needed.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("content"))
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	inst := NewInstaller(nil, DefaultLimits())
	_, err := inst.download(context.Background(), srv.URL, dest)
	if err == nil {
		t.Fatal("expected local storage failure")
	}
	if !errors.Is(err, ErrLocalStorage) {
		t.Fatalf("error must carry ErrLocalStorage: %v", err)
	}
	if errors.Is(err, ErrDownloadFailed) {
		t.Fatalf("local storage failure must not be classified as upstream: %v", err)
	}
	if !strings.Contains(err.Error(), "create dest file") {
		t.Fatalf("error should name the failing phase: %v", err)
	}
}
