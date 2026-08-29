package skills

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zerone-agent/agent-deployer/internal/download"
	"github.com/zerone-agent/agent-deployer/internal/model"
)

// Limits caps resource usage during a single Install call.
// All limits are per-skill, not per-agent.
type Limits struct {
	ZipMaxBytes      int64         // max total bytes of one zip archive
	ZipEntryMaxBytes int64         // max bytes of any single file inside the zip
	ExtractMaxBytes  int64         // max total extracted bytes
	ExtractMaxFiles  int           // max number of files inside the zip
	DownloadTimeout  time.Duration // overall download deadline
}

// DefaultLimits returns the production defaults.
// Zip 100MB, entry 50MB, extract 200MB, 1000 files, 60s download.
func DefaultLimits() Limits {
	return Limits{
		ZipMaxBytes:      100 * 1024 * 1024,
		ZipEntryMaxBytes: 50 * 1024 * 1024,
		ExtractMaxBytes:  200 * 1024 * 1024,
		ExtractMaxFiles:  1000,
		DownloadTimeout:  60 * time.Second,
	}
}

var (
	// ErrHashMismatch is returned when the downloaded zip's actual sha256 does
	// not equal the SkillSource.Hash declared by the client.
	ErrHashMismatch = errors.New("downloaded zip hash does not match declared hash")

	// ErrDownloadFailed wraps any download-time failure: non-2xx HTTP status
	// (redirects are never followed — every 3xx is rejected), network error,
	// timeout, or zip size exceeding ZipMaxBytes. Clients should map this to
	// HTTP 502 (upstream failure). Local storage faults are not wrapped here;
	// they surface via download.ErrLocalStorage instead (handler default: 500).
	ErrDownloadFailed = errors.New("skill download failed")

	// ErrZipSlip wraps extraction failures where a zip entry attempts path
	// traversal: absolute paths, parent traversal, Windows drive letters, or
	// escapes after filepath.Clean. Clients should map this to HTTP 422
	// (the hash the client declared corresponds to a malicious zip).
	ErrZipSlip = errors.New("zip slip attempt detected")

	// ErrSizeExceeded wraps extraction failures where a zip entry, the total
	// extracted bytes, or the entry count exceeds the configured Limits.
	// Clients should map this to HTTP 422.
	ErrSizeExceeded = errors.New("zip size limit exceeded")
)

// Installer downloads and extracts a single skill zip into the per-agent
// skills directory. One Installer can be reused across many Install calls.
type Installer struct {
	client *http.Client
	limits Limits
}

// NewInstaller constructs an Installer. client may be nil (defaults to
// http.DefaultClient). If limits.DownloadTimeout is zero, all limits are
// replaced with DefaultLimits() to avoid network hangs; callers wanting
// custom limits should always set DownloadTimeout.
func NewInstaller(client *http.Client, limits Limits) *Installer {
	if client == nil {
		client = http.DefaultClient
	}
	if limits.DownloadTimeout == 0 {
		// Defense-in-depth: a zero DownloadTimeout would make context.WithTimeout
		// return a context that never expires, hanging forever on a stalled
		// connection. Caller is expected to use DefaultLimits(); this guard
		// catches misuse.
		limits = DefaultLimits()
	}
	return &Installer{client: client, limits: limits}
}

// Install makes source.Name available under skillsDir/<source.Name>/.
//
// The deployer does not inspect or enforce any skill directory contract:
// the zip is downloaded, hash-verified and extracted into skillsDir/<name>/.
// Directory semantics belong to the publisher and the runtime, not to us.
//
// The only layout normalization applied is stripRedundantTopLevelDir: if the
// archive contains a single top-level directory whose name equals source.Name
// (the same name the install directory already uses), the inner directory is
// collapsed up one level to avoid skillsDir/<name>/<name>/... redundancy
// (typical of the CLI's packDir output). This is a name-equality shape fix,
// not a content inspection — it never reads SKILL.md, _meta.json, or any
// other file.
//
// Behavior:
//   - If skillsDir/<name>/.sha256 matches source.NormalizedHash: skip download.
//   - Else: rmrf(skillsDir/<name>), download, verify hash, extract, strip
//     redundant same-name top-level dir, stamp marker, rename into place.
//
// Any failure returns an error; previously-installed skills are left in place.
func (i *Installer) Install(ctx context.Context, source model.SkillSource, skillsDir string) error {
	if err := source.Validate(); err != nil {
		return fmt.Errorf("validate skill %q: %w", source.Name, err)
	}

	localDir := filepath.Join(skillsDir, source.Name)
	incomingHash := source.NormalizedHash()

	// Cache check (spec: timing ①)
	// Cache hit only when .sha256 reads successfully AND matches the incoming
	// hash. Any other case (hash mismatch, missing marker, corrupted state)
	// falls through to a fresh download — os.RemoveAll is a no-op when the
	// path doesn't exist.
	if cached, err := os.ReadFile(filepath.Join(localDir, ".sha256")); err == nil &&
		strings.TrimSpace(string(cached)) == incomingHash {
		return nil // cache hit
	}
	if err := os.RemoveAll(localDir); err != nil {
		return fmt.Errorf("clear stale skill dir %q: %w", localDir, err)
	}

	// 准备工作目录
	tmpRoot := filepath.Join(filepath.Dir(skillsDir), ".skills-tmp")
	if err := os.MkdirAll(tmpRoot, 0755); err != nil {
		return fmt.Errorf("create tmp root: %w", err)
	}
	workDir, err := os.MkdirTemp(tmpRoot, source.Name+"-")
	if err != nil {
		return fmt.Errorf("create work dir: %w", err)
	}
	defer os.RemoveAll(workDir)

	// 下载
	zipPath := filepath.Join(workDir, "skill.zip")
	actualHash, err := download.Fetch(ctx, i.client, source.URL, zipPath, download.Options{
		MaxBytes: i.limits.ZipMaxBytes,
		Timeout:  i.limits.DownloadTimeout,
	})
	if err != nil {
		switch {
		case errors.Is(err, download.ErrLocalStorage):
			// Local disk fault — a deployer-side failure, never an upstream
			// one. The chain carries download.ErrLocalStorage (handler default:
			// 500) and must NOT carry ErrDownloadFailed (→ 502). Previously
			// misclassified as upstream.
			return fmt.Errorf("persist skill %q: %w", source.Name, err)
		case errors.Is(err, download.ErrOversize):
			// Keep the historical 502-class "zip exceeds" message shape.
			err = fmt.Errorf("%w: zip exceeds %d bytes", ErrDownloadFailed, i.limits.ZipMaxBytes)
		default:
			// Network/HTTP errors are upstream failures (502). download.Fetch
			// wraps known cases with ErrFailed; fall back to wrapping any
			// unexpected error with the same sentinel so the handler can map it.
			if !errors.Is(err, ErrDownloadFailed) {
				err = fmt.Errorf("%w: %v", ErrDownloadFailed, err)
			}
		}
		return fmt.Errorf("download skill %q: %w", source.Name, err)
	}

	// Integrity verify (spec: timing ②)
	if actualHash != incomingHash {
		return fmt.Errorf("skill %q: %w (got %s, want %s)", source.Name, ErrHashMismatch, actualHash, incomingHash)
	}

	// 解压到临时目录 extractDir
	extractDir := filepath.Join(workDir, "extract")
	if err := os.MkdirAll(extractDir, 0755); err != nil {
		return fmt.Errorf("create extract dir: %w", err)
	}
	if err := extractZip(zipPath, extractDir, i.limits); err != nil {
		return fmt.Errorf("extract skill %q: %w", source.Name, err)
	}

	// 消除冗余顶层目录：仅当 zip 内恰好有一个与 skill 同名的子目录时，
	// 把它的内容上移一层。这是无语义的形态修正，不读 skill 内容。
	if err := stripRedundantTopLevelDir(extractDir, source.Name); err != nil {
		return fmt.Errorf("strip redundant top-level dir for skill %q: %w", source.Name, err)
	}

	// 写 hash 标记
	hashFile := filepath.Join(extractDir, ".sha256")
	if err := os.WriteFile(hashFile, []byte(incomingHash), 0644); err != nil {
		return fmt.Errorf("write hash marker: %w", err)
	}

	// 原子 rename extractDir -> skillsDir/<name>/
	if err := os.Rename(extractDir, localDir); err != nil {
		return fmt.Errorf("rename skill into place: %w", err)
	}

	return nil
}

// stripRedundantTopLevelDir collapses skillsDir/<name>/<name>/... into
// skillsDir/<name>/... when the archive contains exactly one top-level entry
// that is (a) a directory and (b) named the same as the skill itself. This
// eliminates the nested-directory redundancy produced by the CLI's packDir
// layout without inspecting any file contents.
//
// All other layouts are left untouched:
//   - flat zip (files at root): no-op
//   - single top-level dir with a different name: no-op (preserves publisher's
//     chosen structure)
//   - multiple top-level entries (bundle zips): no-op
//   - top-level mix of files and directories: no-op
func stripRedundantTopLevelDir(extractDir, skillName string) error {
	entries, err := os.ReadDir(extractDir)
	if err != nil {
		return fmt.Errorf("read extract dir: %w", err)
	}
	if len(entries) != 1 || !entries[0].IsDir() || entries[0].Name() != skillName {
		return nil
	}

	innerDir := filepath.Join(extractDir, entries[0].Name())
	innerEntries, err := os.ReadDir(innerDir)
	if err != nil {
		return fmt.Errorf("read inner wrapper dir: %w", err)
	}

	// Move every entry inside the wrapper up one level into extractDir.
	// extractDir currently contains only the wrapper dir, so there can be no
	// name collisions.
	for _, ie := range innerEntries {
		src := filepath.Join(innerDir, ie.Name())
		dst := filepath.Join(extractDir, ie.Name())
		if err := os.Rename(src, dst); err != nil {
			return fmt.Errorf("move %q out of wrapper dir: %w", ie.Name(), err)
		}
	}

	// Wrapper should now be empty; remove it.
	if err := os.Remove(innerDir); err != nil {
		return fmt.Errorf("remove empty wrapper dir: %w", err)
	}
	return nil
}

// extractZip unzips zipPath into dest enforcing Limits.
// Rejects zip slip (absolute paths, '..' segments, Windows drive letters).
func extractZip(zipPath, dest string, limits Limits) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close()

	var totalBytes int64
	var totalFiles int
	for _, f := range r.File {
		if limits.ExtractMaxFiles > 0 && totalFiles >= limits.ExtractMaxFiles {
			return fmt.Errorf("%w: zip has more than %d entries", ErrSizeExceeded, limits.ExtractMaxFiles)
		}
		totalFiles++

		if err := extractEntry(f, dest, limits, &totalBytes); err != nil {
			return err
		}
	}
	return nil
}

// extractEntry writes one zip entry to dest, with all safety checks.
func extractEntry(f *zip.File, dest string, limits Limits, totalBytes *int64) (err error) {
	name := f.Name
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "\\") {
		return fmt.Errorf("%w: absolute path %q", ErrZipSlip, name)
	}
	if len(name) >= 2 && name[1] == ':' && (name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z') {
		return fmt.Errorf("%w: windows drive path %q", ErrZipSlip, name)
	}
	clean := filepath.Clean(filepath.ToSlash(name))
	// NIT-2: reject entries that would collide with the internal .sha256
	// hash marker file the installer writes after extraction. A malicious
	// actor could otherwise poison the marker and force a fake cache hit.
	if clean == ".sha256" {
		return fmt.Errorf("%w: zip entry %q conflicts with internal hash marker", ErrZipSlip, name)
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return fmt.Errorf("%w: path escapes destination: %q", ErrZipSlip, name)
	}
	target := filepath.Join(dest, clean)
	if rel, err := filepath.Rel(dest, target); err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("%w: path escapes destination: %q", ErrZipSlip, name)
	}

	if f.FileInfo().IsDir() {
		return os.MkdirAll(target, 0755)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("mkdir for %q: %w", name, err)
	}

	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry %q: %w", name, err)
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return fmt.Errorf("create %q: %w", name, err)
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	var entryWritten int64
	buf := make([]byte, 32*1024)
	for {
		n, readErr := rc.Read(buf)
		if n > 0 {
			entryWritten += int64(n)
			if limits.ZipEntryMaxBytes > 0 && entryWritten > limits.ZipEntryMaxBytes {
				return fmt.Errorf("%w: entry %q exceeds %d bytes", ErrSizeExceeded, name, limits.ZipEntryMaxBytes)
			}
			*totalBytes += int64(n)
			if limits.ExtractMaxBytes > 0 && *totalBytes > limits.ExtractMaxBytes {
				return fmt.Errorf("%w: total extracted bytes exceed %d", ErrSizeExceeded, limits.ExtractMaxBytes)
			}
			if _, werr := out.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return nil
}
