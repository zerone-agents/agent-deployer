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
