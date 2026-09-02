package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/storage"
)

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

// installClosureTools materializes the custom Tool artifacts declared by every
// agent in the deployment graph (issue #16 extends the issue #10 rule to the
// closure). Any failure aborts Create. Tools land in the shared flat
// <agentsDir>/tools directory; identical declarations across agents download
// once (dedup), while each agent's YAML entry keeps its own customTools
// reference list. It returns the per-agent verified "./tools/..." paths and
// the number of distinct tools installed.
func (s *AgentService) installClosureTools(ctx context.Context, agents []model.AgentDefinition, toolsDir string) (map[string][]string, int, error) {
	installedTools := make(map[string]string) // tool name -> "./tools/<name><ext>"
	agentToolPaths := make(map[string][]string)
	for _, a := range agents {
		for _, src := range a.CustomTools {
			rel, ok := installedTools[src.Name]
			if !ok {
				var err error
				rel, err = s.toolInstaller.Install(ctx, src, toolsDir)
				if err != nil {
					return nil, 0, fmt.Errorf("install tool %q (agent %q): %w", src.Name, a.Name, err)
				}
				installedTools[src.Name] = rel
			}
			if !containsString(agentToolPaths[a.Name], rel) {
				agentToolPaths[a.Name] = append(agentToolPaths[a.Name], rel)
			}
		}
	}
	return agentToolPaths, len(installedTools), nil
}

// installClosureSkills materializes the skill artifacts declared by each agent
// into that agent's own directory: <skillsRoot>/<agentId>/<skillName>.
// Visibility is declared per agent in the YAML via extraUserSkillDirs
// (user-level scan) — no docker cp, no shared project-level directory. Shared
// declarations (same name+url+hash across agents) are installed into each
// declaring agent's own directory: isolation beats download dedup here, and
// model validation guarantees same-name ⇒ same declaration. It reports
// whether any agent in the closure declared skills.
func (s *AgentService) installClosureSkills(ctx context.Context, agents []model.AgentDefinition, skillsRoot string) (bool, error) {
	closureHasSkills := false
	for _, a := range agents {
		if len(a.Skills) == 0 {
			continue
		}
		closureHasSkills = true
		dir := filepath.Join(skillsRoot, a.Name)
		// The installer stages into a sibling temp dir and renames into
		// place; the per-agent directory must exist beforehand.
		if err := storage.EnsureDirs(dir); err != nil {
			return false, fmt.Errorf("create skill directory for agent %q: %w", a.Name, err)
		}
		for _, src := range a.Skills {
			if err := s.skillInstaller.Install(ctx, src, dir); err != nil {
				return false, fmt.Errorf("install skill %q (agent %q): %w", src.Name, a.Name, err)
			}
		}
	}
	return closureHasSkills, nil
}
