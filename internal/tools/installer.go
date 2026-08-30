// Package tools implements the single-file custom Tool installer (issue #10).
// Unlike skills (zip archives extracted into the runtime container), custom
// Tools are individual .ts/.mts/.js/.mjs files installed under the agent
// config directory (bind-mounted at /app/config), so the runtime resolves
// them with relative paths and no extra mounts.
//
// The installer never parses or executes the file content. It downloads (via
// the shared hardened downloader in internal/download), hash-verifies, and
// atomically renames into place; import/schema validation belongs to the
// runtime.
package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zerone-agent/agent-deployer/internal/download"
	"github.com/zerone-agent/agent-deployer/internal/model"
)

// Limits caps resource usage during a single Install call.
type Limits struct {
	MaxBytes        int64         // max bytes of one tool file
	MinBytes        int64         // min bytes of one tool file (rejects empty)
	DownloadTimeout time.Duration // overall download deadline
}

// DefaultLimits returns the production defaults: 5 MiB max, 1 byte min,
// 60s download (issue #10 contract).
func DefaultLimits() Limits {
	return Limits{
		MaxBytes:        5 * 1024 * 1024,
		MinBytes:        1,
		DownloadTimeout: 60 * time.Second,
	}
}

var (
	// ErrHashMismatch is returned when the downloaded file's actual sha256
	// does not equal the ToolSource.Hash declared by the client.
	ErrHashMismatch = errors.New("downloaded tool hash does not match declared hash")

	// ErrDownloadFailed wraps any download-time failure: non-2xx HTTP status
	// (redirects are never followed — every 3xx is rejected), network error,
	// or timeout. Clients should map to HTTP 502. Local storage faults are
	// not wrapped here; they surface via download.ErrLocalStorage instead
	// (handler default: 500).
	ErrDownloadFailed = errors.New("tool download failed")

	// ErrSizeExceeded is returned when the download exceeds MaxBytes.
	// Clients should map to HTTP 422.
	ErrSizeExceeded = errors.New("tool size limit exceeded")

	// ErrEmptyFile is returned when the download is shorter than MinBytes
	// (i.e. empty). Clients should map to HTTP 422.
	ErrEmptyFile = errors.New("tool file is empty")
)

// Installer downloads and verifies single custom Tool files. One Installer
// can be reused across many Install calls.
type Installer struct {
	client *http.Client
	limits Limits
}

// NewInstaller constructs an Installer. client may be nil (defaults to
// http.DefaultClient); regardless, the caller's client is never mutated —
// redirect rejection and the other transport hardening live inside
// download.Fetch, which shallow-copies the client it is handed. A zero
// DownloadTimeout is replaced with DefaultLimits() to avoid network hangs
// (same guard as the skills installer).
func NewInstaller(client *http.Client, limits Limits) *Installer {
	if client == nil {
		client = http.DefaultClient
	}
	if limits.DownloadTimeout == 0 {
		limits = DefaultLimits()
	}
	return &Installer{client: client, limits: limits}
}

// Install makes source.Name available at toolsDir/<Name><Ext> and returns
// the configDir-relative (the parent of toolsDir) agents.yaml path verbatim,
// e.g. "./tools/GetWeather.mjs" (the ToolSource.LocalRelPath value). Callers
// write the returned path into agents.yaml unchanged.
//
// Behavior:
//   - Cache hit (marker file matches AND artifact exists): skip download.
//   - Otherwise: download into a temp dir under toolsDir, stream-hash,
//     verify against the declared hash, atomically rename into place, and
//     stamp a sidecar "<file>.sha256" marker. A changed hash replaces the
//     previous artifact (rename overwrites).
//
// Every failure path cleans up its temp files and leaves the previous
// artifact (if any) untouched.
func (i *Installer) Install(ctx context.Context, source model.ToolSource, toolsDir string) (string, error) {
	if err := source.Validate(); err != nil {
		return "", fmt.Errorf("validate tool %q: %w", source.Name, err)
	}

	localName := source.LocalFileName()
	dest := filepath.Join(toolsDir, localName)
	markerPath := dest + ".sha256"
	want := source.NormalizedHash()
	rel := source.LocalRelPath()

	// Cache hit only when the marker matches AND the artifact still exists.
	// A marker without an artifact (e.g. manual deletion) re-downloads.
	if cached, err := os.ReadFile(markerPath); err == nil &&
		strings.TrimSpace(string(cached)) == want {
		if _, statErr := os.Stat(dest); statErr == nil {
			return rel, nil
		}
	}

	if err := os.MkdirAll(toolsDir, 0755); err != nil {
		return "", fmt.Errorf("create tools dir: %w", err)
	}
	// Temp dir inside toolsDir guarantees the final rename stays on the
	// same filesystem (atomic on POSIX). The runtime never reads it: it
	// only loads explicitly listed customTools paths.
	workDir, err := os.MkdirTemp(toolsDir, ".install-")
	if err != nil {
		return "", fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	tmpPath := filepath.Join(workDir, "tool.bin")
	actual, err := download.Fetch(ctx, i.client, source.URL, tmpPath, download.Options{
		MaxBytes: i.limits.MaxBytes,
		MinBytes: i.limits.MinBytes,
		Timeout:  i.limits.DownloadTimeout,
	})
	if err != nil {
		switch {
		case errors.Is(err, download.ErrLocalStorage):
			// Local disk fault — a deployer-side failure, never an upstream
			// one. The chain carries download.ErrLocalStorage (handler default:
			// 500) and must NOT carry ErrDownloadFailed (→ 502).
			return "", fmt.Errorf("persist tool %q: %w", source.Name, err)
		case errors.Is(err, download.ErrOversize):
			return "", fmt.Errorf("download tool %q: %w: %v", source.Name, ErrSizeExceeded, err)
		case errors.Is(err, download.ErrTooSmall):
			return "", fmt.Errorf("download tool %q: %w: %v", source.Name, ErrEmptyFile, err)
		default:
			// Anything else (incl. download.ErrFailed: transport failure,
			// rejected redirect, non-2xx status, body read error) is an
			// upstream failure.
			if !errors.Is(err, ErrDownloadFailed) {
				err = fmt.Errorf("%w: %v", ErrDownloadFailed, err)
			}
			return "", fmt.Errorf("download tool %q: %w", source.Name, err)
		}
	}

	if actual != want {
		return "", fmt.Errorf("tool %q: %w (got %s, want %s)", source.Name, ErrHashMismatch, actual, want)
	}

	if err := os.Rename(tmpPath, dest); err != nil {
		return "", fmt.Errorf("move tool %q into place: %w", source.Name, err)
	}
	if err := os.WriteFile(markerPath, []byte(want), 0644); err != nil {
		return "", fmt.Errorf("write hash marker for tool %q: %w", source.Name, err)
	}
	return rel, nil
}
