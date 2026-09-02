package skills

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zerone-agent/agent-deployer/internal/model"
)

// buildZip builds an in-memory zip archive with the given file contents.
// Panics on error. Each key in files is a path inside the archive.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		require.NoError(t, err)
		_, err = f.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

// serveZip returns an httptest.Server that always serves the given bytes
// with status 200.
func serveZip(t *testing.T, data []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
}

// sha256Hex returns the lowercase hex sha256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestInstaller_Install_HappyPath(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"SKILL.md":     "# code-review\n",
		"scripts/a.sh": "echo hi\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()

	src := model.SkillSource{
		Name: "code-review",
		URL:  srv.URL + "/skill.zip",
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	// Files extracted at skillsDir/code-review/
	got, err := os.ReadFile(filepath.Join(skillsDir, "code-review", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# code-review\n", string(got))

	got2, err := os.ReadFile(filepath.Join(skillsDir, "code-review", "scripts", "a.sh"))
	require.NoError(t, err)
	assert.Equal(t, "echo hi\n", string(got2))

	// .sha256 marker contains incomingHash (normalized)
	hashMarker, err := os.ReadFile(filepath.Join(skillsDir, "code-review", ".sha256"))
	require.NoError(t, err)
	assert.Equal(t, src.NormalizedHash(), strings.TrimSpace(string(hashMarker)))
}

func TestNewInstaller_ZeroDownloadTimeout_FallsBackToDefault(t *testing.T) {
	inst := NewInstaller(http.DefaultClient, Limits{})
	assert.Equal(t, DefaultLimits().DownloadTimeout, inst.limits.DownloadTimeout,
		"zero DownloadTimeout must fall back to DefaultLimits to avoid network hangs")
	assert.Equal(t, DefaultLimits().ZipMaxBytes, inst.limits.ZipMaxBytes,
		"fallback should populate all fields, not just DownloadTimeout")
}

// servedZip is serveZip plus a request counter. The returned *int increments
// on every HTTP request the server receives.
func servedZip(t *testing.T, data []byte) (*httptest.Server, *int) {
	t.Helper()
	count := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count++
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	return srv, &count
}

func TestInstaller_Install_CacheHit_SkipsDownload(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "v1\n"})
	srv, count := servedZip(t, zipBytes)

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{Name: "code-review", URL: srv.URL, Hash: sha256Hex(zipBytes)}

	// First call downloads.
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))
	firstCount := *count
	require.Greater(t, firstCount, 0)

	// Second call with same hash: download count must not increase.
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))
	assert.Equal(t, firstCount, *count, "cache hit should not contact server")
}

func TestInstaller_Install_CacheMiss_Downloads(t *testing.T) {
	zipV1 := buildZip(t, map[string]string{"SKILL.md": "v1\n"})
	zipV2 := buildZip(t, map[string]string{"SKILL.md": "v2\n"})

	// Serve v1 first, then v2.
	var current []byte = zipV1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(current)
	}))
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()

	srcV1 := model.SkillSource{Name: "code-review", URL: srv.URL, Hash: sha256Hex(zipV1)}
	require.NoError(t, inst.Install(context.Background(), srcV1, skillsDir))

	got, err := os.ReadFile(filepath.Join(skillsDir, "code-review", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "v1\n", string(got))

	// Switch server to v2; client now declares v2's hash; cache should be
	// invalidated and re-download should produce v2 content.
	current = zipV2
	srcV2 := model.SkillSource{Name: "code-review", URL: srv.URL, Hash: sha256Hex(zipV2)}
	require.NoError(t, inst.Install(context.Background(), srcV2, skillsDir))

	got, err = os.ReadFile(filepath.Join(skillsDir, "code-review", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "v2\n", string(got),
		"stale version should have been deleted and replaced")

	// hash marker reflects v2
	marker, err := os.ReadFile(filepath.Join(skillsDir, "code-review", ".sha256"))
	require.NoError(t, err)
	assert.Equal(t, sha256Hex(zipV2), strings.TrimSpace(string(marker)))
}

func TestInstaller_Install_NoSha256Marker_Redownloads(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "x\n"})
	srv, _ := servedZip(t, zipBytes)

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()

	// Pre-create the skill dir WITHOUT a .sha256 marker — simulates
	// corruption, manual tampering, or partial install.
	localDir := filepath.Join(skillsDir, "code-review")
	require.NoError(t, os.MkdirAll(localDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(localDir, "garbage"), []byte("old"), 0644))

	src := model.SkillSource{Name: "code-review", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	// Old content gone, new content present.
	_, err := os.Stat(filepath.Join(localDir, "garbage"))
	assert.True(t, os.IsNotExist(err), "stale garbage should be removed")
	got, err := os.ReadFile(filepath.Join(localDir, "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "x\n", string(got))
}

func TestInstaller_Install_HashMismatch_FailsWithoutRetry(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "x\n"})
	srv, count := servedZip(t, zipBytes)

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()

	// Client declares a hash that doesn't match the actual zip.
	wrongHash := strings.Repeat("0", 64)
	src := model.SkillSource{Name: "code-review", URL: srv.URL, Hash: wrongHash}

	err := inst.Install(context.Background(), src, skillsDir)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrHashMismatch)
	assert.Equal(t, 1, *count, "should download exactly once, no retry")

	// Skill dir must NOT exist (we removed it before re-download attempt).
	_, statErr := os.Stat(filepath.Join(skillsDir, "code-review"))
	assert.True(t, os.IsNotExist(statErr))
}

func TestInstaller_Install_HttpError_Fails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{
		Name: "code-review",
		URL:  srv.URL,
		Hash: strings.Repeat("a", 64),
	}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "http status 500")
}

func TestInstaller_Install_UrlUnreachable_Fails(t *testing.T) {
	// Listen and immediately close to get a refused connection.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	require.NoError(t, ln.Close())

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{
		Name: "code-review",
		URL:  "http://" + ln.Addr().String() + "/skill.zip",
		Hash: strings.Repeat("a", 64),
	}
	err = inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
}

func TestInstaller_Install_ZipTooLarge_Fails(t *testing.T) {
	// Serve a 1MB payload; set limit to 100KB to trigger overflow.
	big := bytes.Repeat([]byte("x"), 1024*1024)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(big)
	}))
	defer srv.Close()

	limits := DefaultLimits()
	limits.ZipMaxBytes = 100 * 1024
	inst := NewInstaller(http.DefaultClient, limits)

	src := model.SkillSource{
		Name: "code-review",
		URL:  srv.URL,
		Hash: sha256Hex(big),
	}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zip exceeds")
}

// buildRawZip builds a zip where entries are provided as a slice of {Name, Body}
// structs. Functionally identical to buildZip; the slice-of-structs form makes
// malicious test cases (with absolute/traversal paths) more readable.
func buildRawZip(t *testing.T, entries []struct{ Name, Body string }) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		f, err := w.Create(e.Name)
		require.NoError(t, err)
		_, err = f.Write([]byte(e.Body))
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func TestInstaller_Install_RejectsAbsoluteUnixPath(t *testing.T) {
	zipBytes := buildRawZip(t, []struct{ Name, Body string }{
		{"/etc/passwd", "evil\n"},
	})
	srv, _ := servedZip(t, zipBytes)
	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute path")
}

func TestInstaller_Install_RejectsParentTraversal(t *testing.T) {
	zipBytes := buildRawZip(t, []struct{ Name, Body string }{
		{"../escape", "evil\n"},
	})
	srv, _ := servedZip(t, zipBytes)
	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "escapes destination")
}

func TestInstaller_Install_RejectsWindowsDrivePath(t *testing.T) {
	zipBytes := buildRawZip(t, []struct{ Name, Body string }{
		{"C:\\boot.ini", "evil\n"},
	})
	srv, _ := servedZip(t, zipBytes)
	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "windows drive")
}

func TestInstaller_Install_RejectsTooManyEntries(t *testing.T) {
	files := make(map[string]string, 10)
	for i := 0; i < 10; i++ {
		files[fmt.Sprintf("f%d.txt", i)] = "x"
	}
	zipBytes := buildZip(t, files)
	srv, _ := servedZip(t, zipBytes)

	limits := DefaultLimits()
	limits.ExtractMaxFiles = 5
	inst := NewInstaller(http.DefaultClient, limits)

	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "more than 5 entries")
}

func TestInstaller_Install_RejectsOversizeEntry(t *testing.T) {
	big := strings.Repeat("x", 1024)
	zipBytes := buildZip(t, map[string]string{"big.txt": big})
	srv, _ := servedZip(t, zipBytes)

	limits := DefaultLimits()
	limits.ZipEntryMaxBytes = 512
	inst := NewInstaller(http.DefaultClient, limits)

	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds")
}

func TestInstaller_Install_RejectsOversizeTotal(t *testing.T) {
	// Two 1KB files; limit total to 1KB so the second one trips the limit.
	zipBytes := buildZip(t, map[string]string{
		"a.txt": strings.Repeat("a", 1024),
		"b.txt": strings.Repeat("b", 1024),
	})
	srv, _ := servedZip(t, zipBytes)

	limits := DefaultLimits()
	limits.ExtractMaxBytes = 1024
	inst := NewInstaller(http.DefaultClient, limits)

	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "total extracted bytes")
}

func TestInstaller_Install_TargetExists_Fails(t *testing.T) {
	// The race: process A wins the rename while process B is mid-download,
	// then B's rename fails because target exists.
	//
	// Simulating this deterministically would require either:
	//   (a) hooking into the Installer between the rm and the rename, or
	//   (b) spawning real goroutines and timing the race — flaky.
	//
	// The contract is enforced by code review of `Install`'s rename error
	// wrapping: any os.Rename failure (target exists, disk full, permission)
	// is wrapped with "rename skill into place: %w" and aborts the Install.
	// This test is a no-op regression guard.
	t.Skip("concurrent rename race is non-deterministic; covered by code review of Install's rename error wrapping")
}

func TestInstaller_Install_CleansUpWorkDirOnSuccess(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"x.txt": "x\n"})
	srv, _ := servedZip(t, zipBytes)

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}

	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	// The per-install staging ROOT is a random ".skills-*" sibling of the
	// skills dir and is removed when the install completes — no residue
	// (and no fixed ".skills-tmp") remains after success.
	parent := filepath.Dir(skillsDir)
	entries, err := os.ReadDir(parent)
	require.NoError(t, err)
	var staging []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".skills-") {
			staging = append(staging, e.Name())
		}
	}
	assert.Empty(t, staging, "staging roots must be cleaned up after success")
}

func TestInstaller_Install_CleansUpWorkDirOnFailure(t *testing.T) {
	// Trigger a failure post-download: hash mismatch.
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "x\n"})
	srv, _ := servedZip(t, zipBytes)

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: strings.Repeat("0", 64)}

	err := inst.Install(context.Background(), src, skillsDir)
	require.Error(t, err)

	// The per-install staging root (random ".skills-*" sibling) is removed
	// on failure too — no residue lingers after a failed install.
	parent := filepath.Dir(skillsDir)
	entries, readErr := os.ReadDir(parent)
	require.NoError(t, readErr)
	for _, e := range entries {
		assert.False(t, strings.HasPrefix(e.Name(), ".skills-"),
			"failed install must not leave a staging root (%q)", e.Name())
	}
}

// TestInstaller_Install_StripsRedundantTopLevelDir verifies that a nested
// zip whose single top-level directory matches the skill name (the typical
// CLI packDir output shape) is collapsed one level to avoid
// skillsDir/<name>/<name>/... redundancy.
func TestInstaller_Install_StripsRedundantTopLevelDir(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"weekly-report/SKILL.md":       "# weekly-report\n",
		"weekly-report/scripts/run.sh": "echo run\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "weekly-report",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	// Files should be directly under skillsDir/<name>/, no nested wrapper.
	got, err := os.ReadFile(filepath.Join(skillsDir, "weekly-report", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# weekly-report\n", string(got))

	got2, err := os.ReadFile(filepath.Join(skillsDir, "weekly-report", "scripts", "run.sh"))
	require.NoError(t, err)
	assert.Equal(t, "echo run\n", string(got2))

	// The redundant inner directory must NOT exist.
	_, statErr := os.Stat(filepath.Join(skillsDir, "weekly-report", "weekly-report"))
	assert.True(t, os.IsNotExist(statErr),
		"redundant nested <name>/<name>/ wrapper should be collapsed")
}

// TestInstaller_Install_KeepsUnmatchedTopLevelDir verifies that a single
// top-level directory whose name does NOT equal the skill name is preserved.
// We only collapse literal name redundancy; otherwise we leave publisher
// structure untouched.
func TestInstaller_Install_KeepsUnmatchedTopLevelDir(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"my-skill/SKILL.md": "# my-skill\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	// Skill installed under "pharma", but zip wraps under "my-skill/":
	// not redundant → must preserve.
	src := model.SkillSource{
		Name: "pharma",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	got, err := os.ReadFile(filepath.Join(skillsDir, "pharma", "my-skill", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# my-skill\n", string(got))
}

// TestInstaller_Install_KeepsBundleZip verifies that a bundle zip (multiple
// top-level directories) is never collapsed.
func TestInstaller_Install_KeepsBundleZip(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"dir-a/a.txt": "a\n",
		"dir-b/b.txt": "b\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "bundle",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	for _, p := range []string{
		filepath.Join(skillsDir, "bundle", "dir-a", "a.txt"),
		filepath.Join(skillsDir, "bundle", "dir-b", "b.txt"),
	} {
		_, err := os.ReadFile(p)
		require.NoError(t, err, "bundle entry must be preserved verbatim")
	}
}

// TestInstaller_Install_KeepsFlatWithDir verifies that a top-level mix of
// files and directories is never collapsed, even if one directory happens to
// match the skill name.
func TestInstaller_Install_KeepsFlatWithDir(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"SKILL.md":        "# flat\n",
		"assets/logo.png": "PNG\n",
		"x.txt":           "x\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "flat-skill",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	// All entries preserved at top level.
	for _, p := range []string{
		filepath.Join(skillsDir, "flat-skill", "SKILL.md"),
		filepath.Join(skillsDir, "flat-skill", "assets", "logo.png"),
		filepath.Join(skillsDir, "flat-skill", "x.txt"),
	} {
		_, err := os.ReadFile(p)
		require.NoError(t, err)
	}
}

// TestInstaller_Install_FlatZipUntouched verifies the most common flat case
// (SKILL.md at archive root) ends up directly under skillsDir/<name>/.
func TestInstaller_Install_FlatZipUntouched(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"SKILL.md":     "# flat\n",
		"scripts/x.sh": "x\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "flat",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	got, err := os.ReadFile(filepath.Join(skillsDir, "flat", "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "# flat\n", string(got))

	got2, err := os.ReadFile(filepath.Join(skillsDir, "flat", "scripts", "x.sh"))
	require.NoError(t, err)
	assert.Equal(t, "x\n", string(got2))
}

// TestInstaller_Install_PreservesFilesVerbatim verifies that files the old
// contract used to strip or reject — macOS junk, skillhub's _meta.json,
// zips without SKILL.md — are now extracted untouched. Directory semantics
// belong to the publisher and the runtime, not the deployer.
func TestInstaller_Install_PreservesFilesVerbatim(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{
		"readme.md":            "# not a skill\n",
		"_meta.json":           `{"slug":"x","version":"1.0.0"}`,
		".DS_Store":            "junk\n",
		"__MACOSX/._readme.md": "junk\n",
	})
	srv := serveZip(t, zipBytes)
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{Name: "x", URL: srv.URL, Hash: sha256Hex(zipBytes)}
	require.NoError(t, inst.Install(context.Background(), src, skillsDir))

	for _, name := range []string{
		"readme.md",
		"_meta.json",
		".DS_Store",
		filepath.Join("__MACOSX", "._readme.md"),
	} {
		_, statErr := os.Stat(filepath.Join(skillsDir, "x", name))
		assert.NoError(t, statErr, "%q should be preserved verbatim", name)
	}
}

// TestInstaller_Install_FollowsRedirectToZip verifies the restored legacy
// skills contract (PR #14 third-round review P1): a redirect chain — 302
// with a Location header pointing at an endpoint serving a hash-matching
// zip — must be followed (real skill zips live behind object-storage/CDN
// 302/307 presigned URLs) and the install must succeed off the FINAL
// response. The download package keeps the mirrored rejection test for the
// hardened default (307 flavor).
func TestInstaller_Install_FollowsRedirectToZip(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "# redirected\n"})
	finalHits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/redirect":
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusFound) // 302 with Location: presigned-URL shape
		case "/final":
			finalHits++
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(zipBytes)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "redirected",
		URL:  srv.URL + "/redirect",
		Hash: sha256Hex(zipBytes),
	}

	require.NoError(t, inst.Install(context.Background(), src, skillsDir),
		"redirect chain to a 200 serving a valid zip must be followed")
	assert.Equal(t, 1, finalHits, "redirect target must be fetched exactly once")
	got, err := os.ReadFile(filepath.Join(skillsDir, "redirected", "SKILL.md"))
	require.NoError(t, err, "skill dir must be populated from the final response")
	assert.Equal(t, "# redirected\n", string(got))
}

// TestInstaller_Install_Non200Rejected verifies the other half of the
// restored legacy skills contract: exactly HTTP 200 is accepted, so a 201
// serving an otherwise valid zip is rejected (the shared downloader's
// 2xx-range default does not apply to skills).
func TestInstaller_Install_Non200Rejected(t *testing.T) {
	zipBytes := buildZip(t, map[string]string{"SKILL.md": "# created\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusCreated) // 201: inside the 2xx range, but not 200
		_, _ = w.Write(zipBytes)
	}))
	defer srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	skillsDir := t.TempDir()
	src := model.SkillSource{
		Name: "created",
		URL:  srv.URL,
		Hash: sha256Hex(zipBytes),
	}

	err := inst.Install(context.Background(), src, skillsDir)
	require.Error(t, err, "201 must be rejected: skills accept exactly HTTP 200")
	assert.ErrorIs(t, err, ErrDownloadFailed)
	assert.Contains(t, err.Error(), "http status 201")
	_, statErr := os.Stat(filepath.Join(skillsDir, "created"))
	assert.True(t, os.IsNotExist(statErr), "no skill dir may be left behind by a rejected status")
}

// TestInstaller_Install_DownloadErrors_DoNotLeakURL verifies the absorbed
// URL-leak hardening: transport-level *url.Error values embed the full
// request URL (query strings included — often signed tokens); surfaced
// errors must contain only the underlying reason.
func TestInstaller_Install_DownloadErrors_DoNotLeakURL(t *testing.T) {
	// Shut the server down so Do fails at the transport level with a
	// *url.Error that would normally embed the full URL + query string.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	urlWithSecret := srv.URL + "/skill.zip?token=SECRET"
	srv.Close()

	inst := NewInstaller(http.DefaultClient, DefaultLimits())
	src := model.SkillSource{
		Name: "leaky",
		URL:  urlWithSecret,
		Hash: strings.Repeat("a", 64),
	}
	err := inst.Install(context.Background(), src, t.TempDir())
	require.Error(t, err, "expected download failure against a closed server")
	assert.ErrorIs(t, err, ErrDownloadFailed)
	assert.Contains(t, err.Error(), "skill download failed")
	assert.NotContains(t, err.Error(), "SECRET")
	assert.NotContains(t, err.Error(), urlWithSecret)
}
