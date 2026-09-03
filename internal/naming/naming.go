package naming

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// DeploymentKey is the deployment resource identity (issue #18): it keys the
// Docker container name and labels, lifecycle lookups, and the per-deployment
// data directories. A distinct named type (issue #20) keeps it from being
// silently swapped with the runtime-side RootAgentID at internal call
// boundaries — the pair travels adjacent through storage and docker
// signatures, where a bare-string swap would still compile.
type DeploymentKey string

// RootAgentID is the runtime agent graph identity (issue #18): the bare root
// agent id written into agents.yaml. Distinct from the tenant-scoped
// DeploymentKey (issue #20).
type RootAgentID string

var invalidChars = regexp.MustCompile(`[^a-zA-Z0-9]+`)

// SanitizeName normalizes arbitrary input into sanitized deployment-key form
// (lowercase alphanumeric + hyphens, trimmed). Generic over ~string so typed
// identities (DeploymentKey) round-trip without explicit conversions while
// plain strings keep working unchanged.
func SanitizeName[T ~string](name T) T {
	s := invalidChars.ReplaceAllString(string(name), "-")
	s = strings.Trim(s, "-")
	return T(strings.ToLower(s))
}

// ContainerName derives the Docker container name from the deployment key.
func ContainerName(prefix string, deploymentKey DeploymentKey, instanceID string) string {
	return prefix + "-" + string(SanitizeName(deploymentKey)) + "-" + instanceID
}

func InstanceID() string {
	return strings.Split(uuid.New().String(), "-")[0]
}

// ContainerSkillsRoot is the in-container root of per-agent skill directories
// (bind-mounted at /app/config; see docker.CreateAgentContainer).
const ContainerSkillsRoot = "/app/config/skills"

// ContainerSkillDir returns the in-container directory holding the skills
// installed for the given agent id.
func ContainerSkillDir(agentID string) string {
	return ContainerSkillsRoot + "/" + agentID
}
