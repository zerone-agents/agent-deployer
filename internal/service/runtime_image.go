package service

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/zerone-agent/agent-deployer/internal/model"
)

// checkRuntimeImage enforces the runtime version floor for the agent graph
// protocol (issue #16): the base floor is v2.4.0, and declaring the
// maxSessionQueries contract key raises it to v2.6.0 (runtime PR #56 / SDK
// 3.1.0 rename — pre-2.6.0 runtimes silently strip the key, so a graph that
// uses it must not deploy onto them). A pinned tag must be semver >= the
// floor; :latest (or an untagged image) is accepted only when the operator
// explicitly assumes it via AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST.
func (s *AgentService) checkRuntimeImage(minMajor, minMinor int) error {
	img := s.cfg.RuntimeImage
	tag := imageTag(img)
	floor := fmt.Sprintf("v%d.%d.0", minMajor, minMinor)
	if tag == "" || tag == "latest" {
		if s.cfg.RuntimeImageAssumeLatest {
			return nil
		}
		return fmt.Errorf("%w: runtime image %q has no version tag; pin AGENT_DEPLOYER_RUNTIME_IMAGE to a %s+ tag, or set AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true if :latest already points to a %s+ build", ErrRuntimeIncompatible, img, floor, floor)
	}
	major, minor, ok := parseMajorMinor(tag)
	if !ok || major < minMajor || (major == minMajor && minor < minMinor) {
		return fmt.Errorf("%w: runtime image tag %q is below the required %s; upgrade the runtime image", ErrRuntimeIncompatible, tag, floor)
	}
	return nil
}

// declaresMaxSessionQueries reports whether any agent in the graph uses the
// maxSessionQueries contract key, which requires runtime v2.6.0+ (SDK 3.1.0
// rename; pre-2.6.0 runtimes silently strip it).
func declaresMaxSessionQueries(agents []model.AgentDefinition) bool {
	for _, a := range agents {
		if a.MaxSessionQueries != nil {
			return true
		}
	}
	return false
}

// imageTag extracts the tag from an image reference (the part after the last
// ":" in the final path segment). Returns "" when untagged.
func imageTag(image string) string {
	last := image
	if i := strings.LastIndex(image, "/"); i >= 0 {
		last = image[i+1:]
	}
	i := strings.LastIndex(last, ":")
	if i < 0 {
		return ""
	}
	return last[i+1:]
}

// parseMajorMinor parses "v2.4.0" / "2.4" style tags.
func parseMajorMinor(tag string) (major, minor int, ok bool) {
	t := strings.TrimPrefix(tag, "v")
	parts := strings.Split(t, ".")
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
