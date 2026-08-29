// Package tools implements the single-file custom Tool installer (issue #10).
// Unlike skills (zip archives extracted into the runtime container), custom
// Tools are individual .ts/.mts/.js/.mjs files installed under the agent
// config directory (bind-mounted at /app/config), so the runtime resolves
// them with relative paths and no extra mounts.
//
// The installer never parses or executes the file content. It downloads,
// hash-verifies, and atomically renames into place; import/schema validation
// belongs to the runtime.
package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// ErrDownloadFailed wraps any download-time failure: non-2xx HTTP
	// status, network error, or timeout. Clients should map to HTTP 502.
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
// http.DefaultClient). A zero DownloadTimeout is replaced with
// DefaultLimits() to avoid network hangs (same guard as the skills installer).
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
// the install path relative to the configDir (the parent of toolsDir),
// e.g. "tools/GetWeather.mjs". Callers write "./"+rel into agents.yaml.
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
	rel := "tools/" + localName

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
	actual, err := i.download(ctx, source.URL, tmpPath)
	if err != nil {
		if !errors.Is(err, ErrDownloadFailed) && !errors.Is(err, ErrSizeExceeded) && !errors.Is(err, ErrEmptyFile) {
			err = fmt.Errorf("%w: %v", ErrDownloadFailed, err)
		}
		return "", fmt.Errorf("download tool %q: %w", source.Name, err)
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

// download streams the URL into dest while computing sha256, enforcing the
// 2xx-only, MaxBytes, MinBytes, and DownloadTimeout limits. It never reads
// or logs the response body beyond hashing/writing it. Returns the actual
// lowercase hex sha256 of the downloaded bytes.
func (i *Installer) download(ctx context.Context, url, dest string) (_ string, err error) {
	dlCtx, cancel := context.WithTimeout(ctx, i.limits.DownloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := i.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: http status %d", ErrDownloadFailed, resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return "", err
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	hasher := sha256.New()
	var written int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			written += int64(n)
			if i.limits.MaxBytes > 0 && written > i.limits.MaxBytes {
				return "", fmt.Errorf("%w: tool exceeds %d bytes", ErrSizeExceeded, i.limits.MaxBytes)
			}
			if _, werr := hasher.Write(buf[:n]); werr != nil {
				return "", werr
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return "", werr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	if i.limits.MinBytes > 0 && written < i.limits.MinBytes {
		return "", fmt.Errorf("%w: got %d bytes", ErrEmptyFile, written)
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}
