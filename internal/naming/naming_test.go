package naming

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeName(t *testing.T) {
	assert.Equal(t, "coder", SanitizeName("coder"))
	assert.Equal(t, "my-agent", SanitizeName("my agent"))
	assert.Equal(t, "a-b-c", SanitizeName("a@b#c"))
	assert.Equal(t, "abc", SanitizeName("_abc_"))
}

func TestContainerName(t *testing.T) {
	name := ContainerName("cloud-agent", "coder", "abc123")
	assert.Equal(t, "cloud-agent-coder-abc123", name)
}

func TestInstanceID(t *testing.T) {
	id := InstanceID()
	assert.Len(t, id, 8)
	assert.NotContains(t, id, "-")
}

func TestContainerSkillDir(t *testing.T) {
	if got := ContainerSkillDir("child-a"); got != "/app/config/skills/child-a" {
		t.Errorf("ContainerSkillDir(\"child-a\") = %q, want /app/config/skills/child-a", got)
	}
}

// TestSanitizeName_PreservesTypedIdentity pins the issue #20 contract:
// SanitizeName is generic over ~string so typed identities round-trip
// without losing their type, and ContainerName accepts the typed key.
func TestSanitizeName_PreservesTypedIdentity(t *testing.T) {
	raw := DeploymentKey("Acme_Assistant")
	got := SanitizeName(raw)
	assert.Equal(t, DeploymentKey("acme-assistant"), got,
		"typed identities must survive sanitization without explicit conversions")

	name := ContainerName("cloud-agent", DeploymentKey("acme-assistant"), "abc123")
	assert.Equal(t, "cloud-agent-acme-assistant-abc123", name)
}
