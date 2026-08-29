package docker

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildEnvVars_OnlyHTTPAPIKey(t *testing.T) {
	env := buildEnvVars("runtime-http-key")
	assert.Equal(t, []string{"ZERONE_AGENT_HTTP_API_KEY=runtime-http-key"}, env,
		"credentials/model now live in agents.yaml; only the runtime's own API key stays in env")
}

func TestBuildEnvVars_EmptyTokenNoEnv(t *testing.T) {
	env := buildEnvVars("")
	assert.Empty(t, env)
}

func TestToRuntimeContainer(t *testing.T) {
	input := types.Container{
		ID:    "abc123",
		Names: []string{"/cloud-agent-coder-aaaaaaaa"},
		Image: "runtime:latest",
		State: "running",
		Labels: map[string]string{
			LabelManaged:    "true",
			LabelAgentName:  "coder",
			LabelInstanceID: "aaaaaaaa",
			LabelCreatedAt:  "2026-06-25T10:00:00Z",
		},
		Ports: []types.Port{
			{IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 0, Type: "tcp"},
			{IP: "0.0.0.0", PrivatePort: 3000, PublicPort: 32768, Type: "tcp"},
		},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "abc123", rc.ID)
	assert.Equal(t, "cloud-agent-coder-aaaaaaaa", rc.Name)
	assert.Equal(t, "coder", rc.AgentName)
	assert.Equal(t, "aaaaaaaa", rc.InstanceID)
	assert.Equal(t, "running", rc.Status)
	assert.Equal(t, "runtime:latest", rc.Image)
	assert.Equal(t, 32768, rc.HostPort)
	assert.Equal(t, "2026-06-25T10:00:00Z", rc.CreatedAt, "CreatedAt must be read from label")
}

func TestToRuntimeContainer_NoPorts(t *testing.T) {
	input := types.Container{
		ID:     "x",
		Names:  []string{"/x"},
		State:  "exited",
		Labels: map[string]string{},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "x", rc.Name)
	assert.Equal(t, "exited", rc.Status)
	assert.Equal(t, 0, rc.HostPort)
	assert.Empty(t, rc.CreatedAt, "CreatedAt must be empty when label is missing")
}

func TestToRuntimeContainer_EmptyName(t *testing.T) {
	input := types.Container{
		ID:     "x",
		Names:  nil,
		Labels: map[string]string{},
	}
	rc := toRuntimeContainer(input)
	assert.Equal(t, "", rc.Name)
}

func TestTarSkillDirs(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "skill-a", "scripts"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "skill-a", "SKILL.md"), []byte("# Skill A"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "skill-a", "scripts", "run.sh"), []byte("echo hi"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "skill-b"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "skill-b", "SKILL.md"), []byte("# Skill B"), 0644))
	// stale skill that should NOT be included
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "stale-skill"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "stale-skill", "SKILL.md"), []byte("stale"), 0644))

	var buf bytes.Buffer
	// Only copy skill-a and skill-b, NOT stale-skill
	require.NoError(t, tarSkillDirs(tmpDir, ".agents/skills", []string{"skill-a", "skill-b"}, &buf))

	tr := tar.NewReader(&buf)
	names := make(map[string]bool)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names[hdr.Name] = true
	}
	assert.True(t, names[".agents/skills/skill-a/SKILL.md"], "expected skill-a/SKILL.md; got: %v", names)
	assert.True(t, names[".agents/skills/skill-a/scripts/run.sh"], "expected skill-a/scripts/run.sh; got: %v", names)
	assert.True(t, names[".agents/skills/skill-b/SKILL.md"], "expected skill-b/SKILL.md; got: %v", names)
	// stale-skill must NOT be in the tar
	for name := range names {
		assert.False(t, strings.Contains(name, "stale-skill"), "stale-skill should not be in tar; found: %s", name)
	}
}

func TestTarSkillDirs_NonExistentSkillSkipped(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "real-skill"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "real-skill", "SKILL.md"), []byte("real"), 0644))

	var buf bytes.Buffer
	// "ghost-skill" doesn't exist on disk — should be silently skipped
	require.NoError(t, tarSkillDirs(tmpDir, ".agents/skills", []string{"real-skill", "ghost-skill"}, &buf))

	tr := tar.NewReader(&buf)
	names := make(map[string]bool)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names[hdr.Name] = true
	}
	assert.True(t, names[".agents/skills/real-skill/SKILL.md"], "real-skill should be in tar")
	for name := range names {
		assert.False(t, strings.Contains(name, "ghost-skill"), "ghost-skill should not be in tar; found: %s", name)
	}
}

func TestTarSkillDirs_EmptyNames(t *testing.T) {
	tmpDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "skill-a"), 0755))

	var buf bytes.Buffer
	require.NoError(t, tarSkillDirs(tmpDir, ".agents/skills", []string{}, &buf))

	tr := tar.NewReader(&buf)
	_, err := tr.Next()
	assert.Equal(t, io.EOF, err, "empty skill names should produce empty tar")
}
