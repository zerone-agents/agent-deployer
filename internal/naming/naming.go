package naming

import (
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var invalidChars = regexp.MustCompile(`[^a-zA-Z0-9]+`)

func SanitizeName(name string) string {
	s := invalidChars.ReplaceAllString(name, "-")
	s = strings.Trim(s, "-")
	return strings.ToLower(s)
}

func ContainerName(prefix, deploymentKey, instanceID string) string {
	return prefix + "-" + SanitizeName(deploymentKey) + "-" + instanceID
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
