// Package service implements the business logic that orchestrates the agent
// container lifecycle. It coordinates Docker operations, filesystem storage of
// agent YAML files, and the naming conventions used to wire everything
// together.
package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"time"

	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/naming"
	"github.com/zerone-agent/agent-deployer/internal/skills"
	"github.com/zerone-agent/agent-deployer/internal/storage"
	"github.com/zerone-agent/agent-deployer/internal/tools"
)

// Sentinel errors that the HTTP handler matches on to pick the right status code.
var (
	// ErrAgentNotFound is returned when no managed container exists for the
	// requested agent name. Handlers should map this to HTTP 404.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrInvalidRequest is returned when the request fails validation.
	// Handlers should map this to HTTP 400.
	ErrInvalidRequest = errors.New("invalid request")

	// ErrRuntimeIncompatible is returned when the configured runtime image
	// cannot run the complete agent graph protocol (requires runtime
	// v2.4.0+). Handlers should map this to HTTP 503.
	ErrRuntimeIncompatible = errors.New("runtime image incompatible with agent graph protocol")
)

// DockerClient captures the subset of the docker.Client surface used by the
// AgentService. Defining it as an interface allows tests to inject a fake.
type DockerClient interface {
	FindAgentContainer(ctx context.Context, deploymentKey naming.DeploymentKey) (*docker.RuntimeContainer, error)
	ListManagedContainers(ctx context.Context) ([]docker.RuntimeContainer, error)
	CreateAgentContainer(ctx context.Context, opts docker.CreateOpts) (string, int, error)
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, containerID string) (*docker.RuntimeContainer, error)
	ContainerLogs(ctx context.Context, id string, tail int) (string, error)
}

// AgentService orchestrates the lifecycle of agent containers, coordinating
// Docker operations, YAML persistence, and naming.
type AgentService struct {
	cfg            *config.Config
	dc             DockerClient
	storage        *storage.AgentStorage
	skillInstaller *skills.Installer
	toolInstaller  *tools.Installer
}

// NewAgentService constructs an AgentService wired to the given config and
// Docker client.
func NewAgentService(cfg *config.Config, dc DockerClient) *AgentService {
	return &AgentService{
		cfg:            cfg,
		dc:             dc,
		storage:        storage.NewAgentStorage(cfg.DataDir),
		skillInstaller: skills.NewInstaller(http.DefaultClient, skills.DefaultLimits()),
		toolInstaller:  tools.NewInstaller(http.DefaultClient, tools.DefaultLimits()),
	}
}

// Config returns the configuration the service was constructed with.
// Exposed primarily for integration tests that need to inspect on-disk paths.
func (s *AgentService) Config() *config.Config { return s.cfg }

// Create creates a new agent container. If an existing container is found for
// the same agent name, the request is treated as idempotent (returning the
// existing container) unless req.Force is set, in which case the old container
// is stopped and removed first.
//
// The returned bool is true when a new container was actually created (or
// recreated via Force), and false when an existing container was returned
// unchanged (idempotent case). Handlers use this to pick HTTP 201 vs 200.
func (s *AgentService) Create(ctx context.Context, req *model.CreateAgentRequest) (*model.AgentResponse, bool, error) {
	if err := model.ValidateCreateRequest(req); err != nil {
		return nil, false, fmt.Errorf("%w: %s", ErrInvalidRequest, err.Error())
	}

	// The complete agent graph protocol requires runtime v2.4.0+; a graph that
	// declares the maxSessionQueries contract key additionally requires v2.6.0+
	// (pre-2.6.0 runtimes strip the key after the SDK 3.1.0 rename — runtime
	// PR #56). Refuse deployment when the configured image cannot be proven
	// compatible instead of silently degrading on an old runtime.
	minMinor := 4
	if declaresMaxSessionQueries(req.Agents) {
		minMinor = 6
	}
	if err := s.checkRuntimeImage(2, minMinor); err != nil {
		return nil, false, err
	}

	// Deployment identity split (issue #18): the deployment key is the sole
	// infra resource identity — Docker labels, container name, filesystem
	// paths, idempotency, lifecycle lookups. rootAgentId is only the
	// runtime-side agent graph identity (agents.yaml root entry).
	deploymentKey := req.DeploymentKey

	existing, err := s.dc.FindAgentContainer(ctx, deploymentKey)
	if err != nil {
		return nil, false, fmt.Errorf("find existing container for deployment %q: %w", deploymentKey, err)
	}

	if existing != nil && !req.Force {
		// Idempotent: return the existing container.
		return s.toResponse(existing), false, nil
	}

	// The runtime token is supplied by the caller and is never generated or persisted by the
	// deployer. It is returned only once in the Create response.
	runtimeToken := req.RuntimeToken

	if existing != nil {
		// Force: stop and remove the old container before creating a new one.
		if err := s.dc.StopContainer(ctx, existing.ID); err != nil {
			return nil, false, fmt.Errorf("stop existing container %s: %w", existing.ID, err)
		}
		if err := s.dc.RemoveContainer(ctx, existing.ID); err != nil {
			return nil, false, fmt.Errorf("remove existing container %s: %w", existing.ID, err)
		}
	}

	instanceID := naming.InstanceID()
	containerName := naming.ContainerName("cloud-agent", deploymentKey, instanceID)

	agentDir := filepath.Join(s.cfg.DataDir, string(deploymentKey), "agents")
	sessionDir := filepath.Join(s.cfg.DataDir, string(deploymentKey), "sessions")
	closureSkillsRoot := filepath.Join(agentDir, "skills")

	if err := storage.EnsureDirs(agentDir, sessionDir); err != nil {
		return nil, false, fmt.Errorf("create agent directories: %w", err)
	}

	// Materialize the closure's artifacts before writing the YAML and creating
	// the container: any install failure aborts Create so an invalid download
	// can never start a container. See artifacts.go for the install layout.
	toolsDir := filepath.Join(agentDir, "tools")
	agentToolPaths, installedToolCount, err := s.installClosureTools(ctx, req.Agents, toolsDir)
	if err != nil {
		return nil, false, err
	}

	if err := s.storage.WriteAgentYAML(deploymentKey, req.RootAgentID, req.Agents, req.Provider, req.Aigc, req.Hub, agentToolPaths); err != nil {
		return nil, false, fmt.Errorf("write agent YAML: %w", err)
	}

	closureHasSkills, err := s.installClosureSkills(ctx, req.Agents, closureSkillsRoot)
	if err != nil {
		return nil, false, err
	}

	opts := docker.CreateOpts{
		DeploymentKey:        deploymentKey,
		RootAgentID:          req.RootAgentID,
		InstanceID:           instanceID,
		ContainerName:        containerName,
		Image:                s.cfg.RuntimeImage,
		RuntimeContainerPort: s.cfg.RuntimeContainerPort,
		AgentDir:             agentDir,
		SessionDir:           sessionDir,
		RuntimeToken:         runtimeToken,
		MemoryBytes:          s.cfg.ContainerMemoryBytes,
		NanoCPUs:             s.cfg.ContainerNanoCPUs,
	}

	containerID, hostPort, err := s.dc.CreateAgentContainer(ctx, opts)
	if err != nil {
		return nil, false, fmt.Errorf("create agent container: %w", err)
	}

	// The response advertises toolsDir/skillsDir only when the graph actually
	// declares those artifacts, keeping artifact-free responses compact.
	toolsDirIfUsed := ""
	if installedToolCount > 0 {
		toolsDirIfUsed = toolsDir
	}
	skillsDirIfUsed := ""
	containerSkillsDirIfUsed := ""
	if closureHasSkills {
		skillsDirIfUsed = closureSkillsRoot
		containerSkillsDirIfUsed = naming.ContainerSkillsRoot
	}

	return &model.AgentResponse{
		AgentName:          req.RootAgentID,
		DeploymentKey:      deploymentKey,
		InstanceID:         instanceID,
		ContainerID:        containerID,
		ContainerName:      containerName,
		Status:             model.StatusRunning,
		HostPort:           hostPort,
		CreatedAt:          time.Now().UTC().Format(time.RFC3339),
		YamlPath:           filepath.Join(agentDir, "agents.yaml"),
		SessionDir:         sessionDir,
		SkillsDir:          skillsDirIfUsed,
		ToolsDir:           toolsDirIfUsed,
		ContainerSkillsDir: containerSkillsDirIfUsed,
		RuntimeToken:       runtimeToken,
	}, true, nil
}

// findManagedContainer resolves a lifecycle path parameter to the managed
// container: sanitize → FindAgentContainer → error wrapping. A nil container
// WITHOUT error means "no such deployment" — each caller decides whether that
// is fatal (lifecycle ops), an archived fallback (Get), or a no-op (Delete).
func (s *AgentService) findManagedContainer(ctx context.Context, name string) (*docker.RuntimeContainer, naming.DeploymentKey, error) {
	deploymentKey := naming.DeploymentKey(naming.SanitizeName(name))
	c, err := s.dc.FindAgentContainer(ctx, deploymentKey)
	if err != nil {
		return nil, deploymentKey, fmt.Errorf("find container for deployment %q: %w", deploymentKey, err)
	}
	return c, deploymentKey, nil
}

// deploymentNotFound builds the standard ErrAgentNotFound error for lifecycle
// lookups that require an existing deployment.
func deploymentNotFound(deploymentKey naming.DeploymentKey) error {
	return fmt.Errorf("deployment %q: %w", deploymentKey, ErrAgentNotFound)
}

// Get returns information about a deployment (keyed by deployment key). If a
// managed container exists, the response reflects the container; otherwise,
// if on-disk data remains (the agent was deleted without purge), the response
// represents the archived agent. Only when neither a container nor data
// exists is ErrAgentNotFound returned.
func (s *AgentService) Get(ctx context.Context, name string) (*model.AgentResponse, error) {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return nil, err
	}
	if c != nil {
		return s.toResponse(c), nil
	}

	// No container: check whether archived data remains on disk.
	if s.storage.Exists(deploymentKey) {
		return s.toArchivedResponse(deploymentKey), nil
	}
	return nil, deploymentNotFound(deploymentKey)
}

// List returns information about all managed agent containers. When
// includeArchived is true, it also includes agents whose container has been
// deleted but whose on-disk data remains (status="archived"). Archived
// entries are merged with active containers by deployment key; if both a
// container and archived data exist for the same key, the active container
// wins. Dedup and sort key on the deployment key (issue #18): two
// deployments may share the same bare rootAgentId and must both appear.
func (s *AgentService) List(ctx context.Context, includeArchived bool) ([]model.AgentResponse, error) {
	containers, err := s.dc.ListManagedContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list managed containers: %w", err)
	}

	responses := make(map[naming.DeploymentKey]model.AgentResponse, len(containers))
	for i := range containers {
		r := s.toResponse(&containers[i])
		responses[r.DeploymentKey] = *r
	}

	if includeArchived {
		dirs, err := s.storage.ListAgentDirs()
		if err != nil {
			return nil, fmt.Errorf("list archived agent dirs: %w", err)
		}
		for _, key := range dirs {
			if _, ok := responses[key]; ok {
				continue // active container already present
			}
			responses[key] = *s.toArchivedResponse(key)
		}
	}

	out := make([]model.AgentResponse, 0, len(responses))
	for _, r := range responses {
		out = append(out, r)
	}
	// Sort by deployment key for stable output.
	sort.Slice(out, func(i, j int) bool {
		return out[i].DeploymentKey < out[j].DeploymentKey
	})
	return out, nil
}

// GetStatus returns the real-time Docker status of a deployment (keyed by
// deployment key), including the health-check result. Clients poll this
// after Create to detect readiness.
func (s *AgentService) GetStatus(ctx context.Context, name string) (*model.AgentStatusResponse, error) {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, deploymentNotFound(deploymentKey)
	}

	// Inspect for health-check status (FindAgentContainer doesn't populate Health).
	detailed, err := s.dc.InspectContainer(ctx, c.ID)
	if err != nil {
		// Fall back to the list-level info if inspect fails.
		return &model.AgentStatusResponse{
			AgentName:     c.RootAgentID,
			DeploymentKey: c.DeploymentKey,
			ContainerName: c.Name,
			ContainerID:   c.ID,
			Status:        string(toStatus(c.Status)),
			Health:        "none",
			HostPort:      c.HostPort,
			Image:         c.Image,
		}, nil
	}

	health := detailed.Health
	if health == "" {
		health = "none"
	}

	return &model.AgentStatusResponse{
		AgentName:     c.RootAgentID,
		DeploymentKey: c.DeploymentKey,
		ContainerName: detailed.Name,
		ContainerID:   detailed.ID,
		Status:        detailed.Status,
		Health:        health,
		HostPort:      detailed.HostPort,
		Image:         detailed.Image,
	}, nil
}

// GetLogs returns the last `tail` lines of the deployment's container output.
func (s *AgentService) GetLogs(ctx context.Context, name string, tail int) (string, error) {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", deploymentNotFound(deploymentKey)
	}
	return s.dc.ContainerLogs(ctx, c.ID, tail)
}

// Stop stops a deployment's container.
func (s *AgentService) Stop(ctx context.Context, name string) error {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return err
	}
	if c == nil {
		return deploymentNotFound(deploymentKey)
	}
	if err := s.dc.StopContainer(ctx, c.ID); err != nil {
		return fmt.Errorf("stop container %s: %w", c.ID, err)
	}
	return nil
}

// Restart restarts a deployment's container.
func (s *AgentService) Restart(ctx context.Context, name string) error {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return err
	}
	if c == nil {
		return deploymentNotFound(deploymentKey)
	}
	if err := s.dc.RestartContainer(ctx, c.ID); err != nil {
		return fmt.Errorf("restart container %s: %w", c.ID, err)
	}
	return nil
}

// Delete removes a deployment's container (keyed by deployment key). By
// default the per-deployment data on disk is preserved so the agent becomes
// "archived" and can still be discovered via List(includeArchived=true).
// When purge is true, the container AND the on-disk data are removed
// (best-effort). Deleting a deployment with neither container nor data is a
// successful no-op.
func (s *AgentService) Delete(ctx context.Context, name string, purge bool) error {
	c, deploymentKey, err := s.findManagedContainer(ctx, name)
	if err != nil {
		return err
	}

	if c != nil {
		if err := s.dc.StopContainer(ctx, c.ID); err != nil {
			return fmt.Errorf("stop container %s: %w", c.ID, err)
		}
		if err := s.dc.RemoveContainer(ctx, c.ID); err != nil {
			return fmt.Errorf("remove container %s: %w", c.ID, err)
		}
	}

	if purge {
		// Remove the entire per-deployment directory (agents + sessions + skills).
		_ = storage.RemoveAll(filepath.Join(s.cfg.DataDir, string(deploymentKey)))
	}

	return nil
}

// toResponse maps a RuntimeContainer to an AgentResponse, filling the
// persistent path fields (YamlPath / SessionDir / SkillsDir) from cfg.DataDir
// keyed by the deployment key, the agentName from the root-id label (empty
// on pre-split containers), and the CreatedAt timestamp from the Docker
// label. Unknown Docker statuses are normalized to model.StatusUnknown.
func (s *AgentService) toResponse(c *docker.RuntimeContainer) *model.AgentResponse {
	agentDir := filepath.Join(s.cfg.DataDir, string(c.DeploymentKey), "agents")
	return &model.AgentResponse{
		AgentName:     c.RootAgentID,
		DeploymentKey: c.DeploymentKey,
		InstanceID:    c.InstanceID,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Status:        toStatus(c.Status),
		HostPort:      c.HostPort,
		CreatedAt:     c.CreatedAt,
		YamlPath:      filepath.Join(agentDir, "agents.yaml"),
		SessionDir:    filepath.Join(s.cfg.DataDir, string(c.DeploymentKey), "sessions"),
		SkillsDir:     filepath.Join(agentDir, "skills"),
	}
}

// toStatus converts a raw Docker state string into an AgentStatus.
func toStatus(raw string) model.AgentStatus {
	switch model.AgentStatus(raw) {
	case model.StatusRunning, model.StatusStopped, model.StatusExited, model.StatusNotFound:
		return model.AgentStatus(raw)
	default:
		return model.StatusUnknown
	}
}

// toArchivedResponse builds an AgentResponse for a deployment whose container
// is gone but whose on-disk data remains. It is used by Get/List when
// includeArchived is true. All container-derived fields are empty, status is
// "archived", and the entry is keyed by deployment key alone — the bare root
// agent id is not recoverable without parsing agents.yaml.
func (s *AgentService) toArchivedResponse(deploymentKey naming.DeploymentKey) *model.AgentResponse {
	return &model.AgentResponse{
		AgentName:     "",
		DeploymentKey: deploymentKey,
		Status:        model.StatusArchived,
		YamlPath:      filepath.Join(s.cfg.DataDir, string(deploymentKey), "agents", "agents.yaml"),
		SessionDir:    filepath.Join(s.cfg.DataDir, string(deploymentKey), "sessions"),
		SkillsDir:     filepath.Join(s.cfg.DataDir, string(deploymentKey), "agents", "skills"),
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
