// Package download implements the shared hardened artifact downloader used by
// the skill and tool installers: redirects are never followed, only final 2xx
// statuses are accepted, *url.Error wrappers are stripped so surfaced errors
// never leak request URLs (incl. signed query strings), and size limits are
// enforced while streaming.
package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"
)

// Options bounds a single Fetch call. The zero value downloads without size
// limits and without a deadline.
type Options struct {
	// MaxBytes caps the streamed body size; 0 = unlimited.
	MaxBytes int64
	// MinBytes requires the body to contain at least this many bytes;
	// 0 = no minimum (an empty body is allowed).
	MinBytes int64
	// Timeout is the overall download deadline; 0 = no deadline.
	Timeout time.Duration
}

var (
	// ErrFailed wraps transport failures and non-2xx final statuses
	// (including rejected 3xx). Callers map to their download-failed
	// sentinel (→ HTTP 502).
	ErrFailed = errors.New("download failed")

	// ErrOversize is returned when the streamed body exceeded
	// Options.MaxBytes.
	ErrOversize = errors.New("download exceeds size limit")

	// ErrTooSmall is returned when the body is shorter than
	// Options.MinBytes.
	ErrTooSmall = errors.New("download is smaller than minimum size")

	// ErrLocalStorage wraps local filesystem failures while writing the
	// destination file (create/write/close). Deployer-side fault — callers
	// must NOT classify it as upstream (HTTP 500, not 502).
	ErrLocalStorage = errors.New("local storage failure")
)

// Fetch streams rawURL into the file at dest while computing SHA-256 and
// returns the lowercase hex digest.
//
// Hardening (binding for every caller):
//   - client is never used or mutated directly: a shallow copy with redirect
//     following disabled is made instead (net/http has no Client.Clone; the
//     shallow copy is the canonical idiom), so a 3xx first response is
//     returned as-is and rejected by the 2xx-only status check — its
//     Location, if any, is never fetched.
//   - *url.Error wrappers from both request construction and transport are
//     stripped to their underlying cause so surfaced errors never embed the
//     request URL (which often carries signed query strings).
func Fetch(ctx context.Context, client *http.Client, rawURL, dest string, opts Options) (_ string, err error) {
	if client == nil {
		client = http.DefaultClient
	}
	// Redirects must be rejected, not followed. ErrUseLastResponse makes Do
	// return the 3xx response itself, which the 2xx-only status check below
	// then rejects. The shallow copy carries over Transport/Jar/Timeout while
	// guaranteeing the caller's client is never mutated (the shared Transport
	// is safe — it is never written).
	noRedirect := *client
	noRedirect.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	// A literal context.WithTimeout(ctx, 0) would already be expired, so the
	// zero value means "leave the parent deadline untouched".
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		// url.Parse failures are *url.Error values embedding the raw URL.
		// Keep only the underlying reason (never leak URL details).
		return "", fmt.Errorf("%w: %v", ErrFailed, stripURLError(err))
	}
	resp, err := noRedirect.Do(req)
	if err != nil {
		// *url.Error embeds the full request URL (incl. query string, often a
		// signed token) in its message. Keep only the underlying reason.
		return "", fmt.Errorf("%w: %v", ErrFailed, stripURLError(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: http status %d", ErrFailed, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", fmt.Errorf("%w: create dest file: %v", ErrLocalStorage, err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("%w: close dest file: %v", ErrLocalStorage, cerr)
		}
	}()

	hasher := sha256.New()
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if opts.MaxBytes > 0 && written > opts.MaxBytes {
				return "", fmt.Errorf("%w: streamed %d bytes (limit %d)", ErrOversize, written, opts.MaxBytes)
			}
			// hash.Hash.Write is documented to never return an error.
			_, _ = hasher.Write(buf[:n])
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", fmt.Errorf("%w: write dest file: %v", ErrLocalStorage, werr)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", fmt.Errorf("%w: read body: %v", ErrFailed, readErr)
		}
	}
	if opts.MinBytes > 0 && written < opts.MinBytes {
		return "", fmt.Errorf("%w: got %d bytes (minimum %d)", ErrTooSmall, written, opts.MinBytes)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// stripURLError unwraps *url.Error to its underlying cause. Parse errors and
// transport errors both embed the full raw URL (query strings included) in
// their message; returning only .Err guarantees surfaced errors never leak it.
func stripURLError(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}
