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

func ContainerName(prefix, agentName, instanceID string) string {
	return prefix + "-" + SanitizeName(agentName) + "-" + instanceID
}

func InstanceID() string {
	return strings.Split(uuid.New().String(), "-")[0]
}
