package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// serveBody starts a server returning body with the given status.
func serveBody(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestFetch_Success_MultiChunk(t *testing.T) {
	// 96 KiB forces multiple 32 KiB stream chunks. The nil client also covers
	// the http.DefaultClient fallback.
	body := bytes.Repeat([]byte("0123456789abcdef"), 6*1024)
	srv := serveBody(t, http.StatusOK, body)

	dest := filepath.Join(t.TempDir(), "out.bin")
	got, err := Fetch(context.Background(), nil, srv.URL+"/a.js?token=x", dest,
		Options{MaxBytes: 1 << 20, MinBytes: 1, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if want := hashOf(body); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
	onDisk, rerr := os.ReadFile(dest)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(onDisk, body) {
		t.Fatalf("on-disk content mismatch: got %d bytes, want %d", len(onDisk), len(body))
	}
}

func TestFetch_ExactMaxBytesPasses_OversizeRejected(t *testing.T) {
	exact := []byte(strings.Repeat("x", 64))
	srv := serveBody(t, http.StatusOK, exact)
	dest := filepath.Join(t.TempDir(), "exact.bin")
	if _, err := Fetch(context.Background(), srv.Client(), srv.URL, dest, Options{MaxBytes: 64}); err != nil {
		t.Fatalf("exactly MaxBytes must pass: %v", err)
	}

	over := []byte(strings.Repeat("x", 65))
	srv2 := serveBody(t, http.StatusOK, over)
	dest2 := filepath.Join(t.TempDir(), "over.bin")
	if _, err := Fetch(context.Background(), srv2.Client(), srv2.URL, dest2, Options{MaxBytes: 64}); !errors.Is(err, ErrOversize) {
		t.Fatalf("err = %v, want ErrOversize", err)
	}
}

func TestFetch_EmptyBodyBelowMinBytesRejected(t *testing.T) {
	srv := serveBody(t, http.StatusOK, nil)
	dest := filepath.Join(t.TempDir(), "empty.bin")
	_, err := Fetch(context.Background(), srv.Client(), srv.URL, dest, Options{MinBytes: 1})
	if !errors.Is(err, ErrTooSmall) {
		t.Fatalf("err = %v, want ErrTooSmall", err)
	}
}

func TestFetch_Non2xxRejected(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			srv := serveBody(t, status, []byte("x"))
			_, err := Fetch(context.Background(), srv.Client(), srv.URL, filepath.Join(t.TempDir(), "o.bin"), Options{})
			if !errors.Is(err, ErrFailed) {
				t.Fatalf("err = %v, want ErrFailed", err)
			}
			if want := fmt.Sprintf("http status %d", status); !strings.Contains(err.Error(), want) {
				t.Fatalf("error should report status %q: %v", want, err)
			}
		})
	}
}

func TestFetch_RedirectTo2xxRejected(t *testing.T) {
	// A real redirect chain (307 with Location pointing at a 200 endpoint)
	// must NOT be followed; the 3xx first response is the final one.
	finalHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "/final":
			finalHits++
			_, _ = w.Write([]byte("content"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	_, err := Fetch(context.Background(), srv.Client(), srv.URL+"/redirect", filepath.Join(t.TempDir(), "o.bin"), Options{})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if !strings.Contains(err.Error(), "http status 307") {
		t.Fatalf("error should report the rejected 3xx status: %v", err)
	}
	if finalHits != 0 {
		t.Fatalf("redirect target must never be requested (hits = %d)", finalHits)
	}
}

func TestFetch_FollowRedirects_307To2xxSucceeds(t *testing.T) {
	// Mirror of TestFetch_RedirectTo2xxRejected with the legacy switch set:
	// FollowRedirects lets the caller's client policy apply (nil
	// CheckRedirect = Go default: follow), so the 307 is followed to the
	// 200 endpoint and the FINAL response body is fetched and hashed.
	final := []byte("redirected content")
	finalHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect)
		case "/final":
			finalHits++
			_, _ = w.Write(final)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	got, err := Fetch(context.Background(), srv.Client(), srv.URL+"/redirect", dest, Options{FollowRedirects: true})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if finalHits != 1 {
		t.Fatalf("redirect target must be fetched exactly once (hits = %d)", finalHits)
	}
	if want := hashOf(final); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
	onDisk, rerr := os.ReadFile(dest)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(onDisk, final) {
		t.Fatalf("on-disk content mismatch: got %q, want %q", onDisk, final)
	}
}

func TestFetch_RequireStatusOK_Rejects201(t *testing.T) {
	// 201 is a success status in the 2xx range but must be rejected when
	// the caller narrows acceptance to exactly 200 (legacy skills contract).
	body := []byte("created")
	srv := serveBody(t, http.StatusCreated, body)
	_, err := Fetch(context.Background(), srv.Client(), srv.URL, filepath.Join(t.TempDir(), "o.bin"), Options{RequireStatusOK: true})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if !strings.Contains(err.Error(), "http status 201") {
		t.Fatalf("error should report status 201: %v", err)
	}
}

func TestFetch_Default_Accepts201(t *testing.T) {
	// Default (RequireStatusOK=false): any final 2xx is accepted (issue #10
	// tools contract: "reject non-2xx responses").
	body := []byte("created")
	srv := serveBody(t, http.StatusCreated, body)
	dest := filepath.Join(t.TempDir(), "o.bin")
	got, err := Fetch(context.Background(), srv.Client(), srv.URL, dest, Options{})
	if err != nil {
		t.Fatalf("201 must be accepted by default: %v", err)
	}
	if want := hashOf(body); got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestFetch_TransportFailure_DoesNotLeakURL(t *testing.T) {
	// Shut the server down so Do fails at the transport level with a
	// *url.Error that would normally embed the full URL + query string.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	urlWithSecret := srv.URL + "/t.js?token=SECRET"
	srv.Close()

	_, err := Fetch(context.Background(), http.DefaultClient, urlWithSecret, filepath.Join(t.TempDir(), "o.bin"), Options{})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), urlWithSecret) {
		t.Fatalf("error leaks URL details: %v", err)
	}
}

func TestFetch_ParseFailure_DoesNotLeakURL(t *testing.T) {
	// http.NewRequestWithContext surfaces url.Parse failures as *url.Error
	// values embedding the raw URL; Fetch must strip to the reason only.
	_, err := Fetch(context.Background(), http.DefaultClient, "http://ex ample.com/t.js?token=SECRET",
		filepath.Join(t.TempDir(), "o.bin"), Options{})
	if !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v, want ErrFailed", err)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "ex ample.com") {
		t.Fatalf("error leaks URL details: %v", err)
	}
}

func TestFetch_LocalStorageFailure_NotUpstreamFailure(t *testing.T) {
	// os.Create fails with EISDIR when dest is an existing directory —
	// deterministic local-storage failure, no network needed.
	srv := serveBody(t, http.StatusOK, []byte("content"))

	dest := filepath.Join(t.TempDir(), "out")
	if err := os.Mkdir(dest, 0755); err != nil {
		t.Fatal(err)
	}
	_, err := Fetch(context.Background(), srv.Client(), srv.URL, dest, Options{})
	if !errors.Is(err, ErrLocalStorage) {
		t.Fatalf("error must carry ErrLocalStorage: %v", err)
	}
	if errors.Is(err, ErrFailed) {
		t.Fatalf("local storage failure must not be classified as upstream: %v", err)
	}
	if !strings.Contains(err.Error(), "create dest file") {
		t.Fatalf("error should name the failing phase: %v", err)
	}
}

func TestFetch_DoesNotMutateCallerClient(t *testing.T) {
	caller := &http.Client{} // nil CheckRedirect
	srv := serveBody(t, http.StatusInternalServerError, []byte("x"))

	if _, err := Fetch(context.Background(), caller, srv.URL, filepath.Join(t.TempDir(), "o.bin"), Options{}); err == nil {
		t.Fatal("expected fetch failure")
	}
	if caller.CheckRedirect != nil {
		t.Fatal("caller's CheckRedirect must remain untouched")
	}
}
