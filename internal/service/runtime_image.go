package service

import (
	"fmt"
	"strconv"
	"strings"
)

// checkRuntimeImage enforces the runtime v2.4.0+ floor for the agent graph
// protocol (issue #16): a pinned tag must be semver >= 2.4.0; :latest (or an
// untagged image) is accepted only when the operator explicitly assumes it
// via AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST.
func (s *AgentService) checkRuntimeImage() error {
	img := s.cfg.RuntimeImage
	tag := imageTag(img)
	if tag == "" || tag == "latest" {
		if s.cfg.RuntimeImageAssumeLatest {
			return nil
		}
		return fmt.Errorf("%w: runtime image %q has no version tag; pin AGENT_DEPLOYER_RUNTIME_IMAGE to a v2.4.0+ tag, or set AGENT_DEPLOYER_RUNTIME_IMAGE_ASSUME_LATEST=true if :latest already points to a v2.4.0+ build", ErrRuntimeIncompatible, img)
	}
	major, minor, ok := parseMajorMinor(tag)
	if !ok || major < 2 || (major == 2 && minor < 4) {
		return fmt.Errorf("%w: runtime image tag %q is below the required v2.4.0; upgrade the runtime image", ErrRuntimeIncompatible, tag)
	}
	return nil
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
