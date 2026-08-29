// Package service implements the business logic that orchestrates the agent
// container lifecycle. It coordinates Docker operations, filesystem storage of
// agent YAML files, and the naming conventions used to wire everything
// together.
package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/zerone-agent/agent-deployer/internal/config"
	"github.com/zerone-agent/agent-deployer/internal/docker"
	"github.com/zerone-agent/agent-deployer/internal/model"
	"github.com/zerone-agent/agent-deployer/internal/naming"
	"github.com/zerone-agent/agent-deployer/internal/skills"
	"github.com/zerone-agent/agent-deployer/internal/storage"
)

// Sentinel errors that the HTTP handler matches on to pick the right status code.
var (
	// ErrAgentNotFound is returned when no managed container exists for the
	// requested agent name. Handlers should map this to HTTP 404.
	ErrAgentNotFound = errors.New("agent not found")

	// ErrInvalidRequest is returned when the request fails validation.
	// Handlers should map this to HTTP 400.
	ErrInvalidRequest = errors.New("invalid request")
)

// DockerClient captures the subset of the docker.Client surface used by the
// AgentService. Defining it as an interface allows tests to inject a fake.
type DockerClient interface {
	FindAgentContainer(ctx context.Context, agentName string) (*docker.RuntimeContainer, error)
	ListManagedContainers(ctx context.Context) ([]docker.RuntimeContainer, error)
	CreateAgentContainer(ctx context.Context, opts docker.CreateOpts) (string, int, error)
	StopContainer(ctx context.Context, id string) error
	RestartContainer(ctx context.Context, id string) error
	RemoveContainer(ctx context.Context, id string) error
	InspectContainer(ctx context.Context, containerID string) (*docker.RuntimeContainer, error)
	ContainerLogs(ctx context.Context, id string, tail int) (string, error)
	CopySkillsToContainer(ctx context.Context, containerID, sourceDir string, skillNames []string) error
}

// AgentService orchestrates the lifecycle of agent containers, coordinating
// Docker operations, YAML persistence, and naming.
type AgentService struct {
	cfg       *config.Config
	dc        DockerClient
	storage   *storage.AgentStorage
	installer *skills.Installer

	// dialTCP dials the upstream locator for reachability probing. Injectable
	// for tests; production default is a 1s TCP dial.
	dialTCP func(ctx context.Context, addr string) error
}

// NewAgentService constructs an AgentService wired to the given config and
// Docker client.
func NewAgentService(cfg *config.Config, dc DockerClient) *AgentService {
	svc := &AgentService{
		cfg:       cfg,
		dc:        dc,
		storage:   storage.NewAgentStorage(cfg.DataDir),
		installer: skills.NewInstaller(http.DefaultClient, skills.DefaultLimits()),
		dialTCP: func(ctx context.Context, addr string) error {
			d := net.Dialer{Timeout: time.Second}
			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return err
			}
			return conn.Close()
		},
	}
	return svc
}

// Config returns the configuration the service was constructed with.
// Exposed primarily for integration tests that need to inspect on-disk paths.
func (s *AgentService) Config() *config.Config { return s.cfg }

// runtimeBindHost returns the host IP that runtime container ports are
// published on. Empty string means "do not publish any host port"
// (docker-network topology).
func (s *AgentService) runtimeBindHost() string {
	switch s.cfg.RuntimeExpose {
	case config.ExposeLoopback:
		return "127.0.0.1"
	case config.ExposePrivate:
		return s.cfg.RuntimeBindIP
	case config.ExposeDockerNetwork:
		return ""
	default: // public and zero value
		return "0.0.0.0"
	}
}

// upstreamFor derives the trusted internal locator for a runtime container
// from the configured topology and live container state (issue #11). It never
// reads client input. It returns nil — never a stale or guessed address —
// when no valid locator can be derived (fail closed):
//
//   - public topology: locators are not emitted at all
//   - any other topology: the container must be running — Docker retains
//     port bindings for stopped containers, but the process is down, so a
//     non-running container yields no locator in every topology (fail
//     closed)
//   - loopback/private: the container must additionally have a published
//     host port
//   - docker-network: the container must additionally be attached to the
//     configured shared network (containers from before a topology switch
//     fail closed and must be force-redeployed)
//
// The derived locator is re-validated as defense in depth.
func (s *AgentService) upstreamFor(c *docker.RuntimeContainer) *model.Upstream {
	var u model.Upstream
	switch s.cfg.RuntimeExpose {
	case config.ExposeLoopback, config.ExposePrivate:
		if c.Status != "running" {
			return nil
		}
		if c.HostPort == 0 {
			return nil
		}
		u = model.Upstream{Scheme: "http", Port: c.HostPort}
		switch {
		case s.cfg.UpstreamHost != "":
			u.Host = s.cfg.UpstreamHost
		case s.cfg.RuntimeExpose == config.ExposeLoopback:
			u.Host = "127.0.0.1"
		default:
			u.Host = s.cfg.RuntimeBindIP
		}
	case config.ExposeDockerNetwork:
		if c.Status != "running" {
			return nil
		}
		if !slices.Contains(c.Networks, s.cfg.RuntimeNetwork) {
			return nil
		}
		u = model.Upstream{Scheme: "http", Host: c.Name, Port: s.cfg.RuntimeContainerPort}
	default: // public: no locator, ever
		return nil
	}
	if err := u.Validate(); err != nil {
		// Defense in depth: a config/container combination that would produce
		// an invalid locator yields no locator at all.
		return nil
	}
	return &u
}

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

	// Sanitize the agent name once and use it consistently everywhere: Docker
	// labels, filesystem paths, and the YAML identity. The sanitized name is
	// what we treat as the canonical agent identifier.
	agentName := naming.SanitizeName(req.Agent.Name)

	existing, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return nil, false, fmt.Errorf("find existing container for agent %q: %w", agentName, err)
	}

	if existing != nil && !req.Force {
		// Idempotent: return the existing container.
		return s.toResponse(existing), false, nil
	}

	// The runtime token is supplied by the caller and is never generated or
	// persisted by the deployer. It is returned only once in the Create response.
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
	containerName := naming.ContainerName("cloud-agent", agentName, instanceID)

	agentDir := filepath.Join(s.cfg.DataDir, agentName, "agents")
	sessionDir := filepath.Join(s.cfg.DataDir, agentName, "sessions")
	skillsDir := filepath.Join(s.cfg.DataDir, agentName, "skills")

	if err := storage.EnsureDirs(agentDir, sessionDir, skillsDir); err != nil {
		return nil, false, fmt.Errorf("create agent directories: %w", err)
	}

	// The storage layer validates that agent.Name == name. Since we use the
	// sanitized name as the canonical identity, set it on the agent copy
	// before writing the YAML.
	agent := req.Agent
	agent.Name = agentName

	if err := s.storage.WriteAgentYAML(agentName, agent, req.Provider, req.Aigc, req.Hub); err != nil {
		return nil, false, fmt.Errorf("write agent YAML: %w", err)
	}

	// Install all declared skills before creating the container. Failure of
	// any skill aborts Create; the container is not created. Skills that
	// already downloaded successfully are left on disk as a valid cache.
	for _, src := range agent.Skills {
		if err := s.installer.Install(ctx, src, skillsDir); err != nil {
			return nil, false, fmt.Errorf("install skill %q: %w", src.Name, err)
		}
	}

	opts := docker.CreateOpts{
		AgentName:            agentName,
		InstanceID:           instanceID,
		ContainerName:        containerName,
		Image:                s.cfg.RuntimeImage,
		RuntimeContainerPort: s.cfg.RuntimeContainerPort,
		AgentDir:             agentDir,
		SessionDir:           sessionDir,
		Agent:                agent,
		Provider:             req.Provider,
		RuntimeToken:         runtimeToken,
		MemoryBytes:          s.cfg.ContainerMemoryBytes,
		NanoCPUs:             s.cfg.ContainerNanoCPUs,
		BindHost:             s.runtimeBindHost(),
		NetworkName:          s.cfg.RuntimeNetwork,
	}

	containerID, hostPort, err := s.dc.CreateAgentContainer(ctx, opts)
	if err != nil {
		return nil, false, fmt.Errorf("create agent container: %w", err)
	}

	// Inject skills into the container's project-level directory via docker cp.
	// This happens after container creation because skills are no longer
	// bind-mounted. If cp fails, clean up the container to avoid leaving a
	// half-configured agent, then return the error.
	if len(agent.Skills) > 0 {
		skillNames := make([]string, len(agent.Skills))
		for i, s := range agent.Skills {
			skillNames[i] = s.Name
		}
		if err := s.dc.CopySkillsToContainer(ctx, containerID, skillsDir, skillNames); err != nil {
			_ = s.dc.StopContainer(ctx, containerID)
			_ = s.dc.RemoveContainer(ctx, containerID)
			return nil, false, fmt.Errorf("copy skills to container: %w", err)
		}
	}

	// Derive the locator from the container we just created. The synthetic
	// RuntimeContainer mirrors what a subsequent Get would find.
	fresh := &docker.RuntimeContainer{
		Name:     containerName,
		Status:   "running",
		HostPort: hostPort,
	}
	if s.cfg.RuntimeNetwork != "" {
		fresh.Networks = []string{s.cfg.RuntimeNetwork}
	}

	return &model.AgentResponse{
		AgentName:     agentName,
		InstanceID:    instanceID,
		ContainerID:   containerID,
		ContainerName: containerName,
		Status:        model.StatusRunning,
		HostPort:      hostPort,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339),
		YamlPath:      filepath.Join(agentDir, "agents.yaml"),
		SessionDir:    sessionDir,
		SkillsDir:     skillsDir,
		RuntimeToken:  runtimeToken,
		Upstream:      s.upstreamFor(fresh),
	}, true, nil
}

// Get returns information about an agent. If a managed container exists, the
// response reflects the container; otherwise, if on-disk data remains (the
// agent was deleted without purge), the response represents the archived
// agent. Only when neither a container nor data exists is ErrAgentNotFound
// returned.
func (s *AgentService) Get(ctx context.Context, name string) (*model.AgentResponse, error) {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("find container for agent %q: %w", agentName, err)
	}
	if c != nil {
		return s.toResponse(c), nil
	}

	// No container: check whether archived data remains on disk.
	if s.storage.Exists(agentName) {
		return s.toArchivedResponse(agentName), nil
	}
	return nil, fmt.Errorf("agent %q: %w", agentName, ErrAgentNotFound)
}

// List returns information about all managed agent containers. When
// includeArchived is true, it also includes agents whose container has been
// deleted but whose on-disk data remains (status="archived"). Archived entries
// are merged with active containers by agent name; if both a container and
// archived data exist for the same name, the active container wins.
func (s *AgentService) List(ctx context.Context, includeArchived bool) ([]model.AgentResponse, error) {
	containers, err := s.dc.ListManagedContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list managed containers: %w", err)
	}

	responses := make(map[string]model.AgentResponse, len(containers))
	for i := range containers {
		r := s.toResponse(&containers[i])
		responses[r.AgentName] = *r
	}

	if includeArchived {
		dirs, err := s.storage.ListAgentDirs()
		if err != nil {
			return nil, fmt.Errorf("list archived agent dirs: %w", err)
		}
		for _, name := range dirs {
			if _, ok := responses[name]; ok {
				continue // active container already present
			}
			responses[name] = *s.toArchivedResponse(name)
		}
	}

	out := make([]model.AgentResponse, 0, len(responses))
	for _, r := range responses {
		out = append(out, r)
	}
	// Sort by agent name for stable output.
	sort.Slice(out, func(i, j int) bool {
		return out[i].AgentName < out[j].AgentName
	})
	return out, nil
}

// GetStatus returns the real-time Docker status of an agent, including the
// health-check result. Clients poll this after Create to detect readiness.
func (s *AgentService) GetStatus(ctx context.Context, name string) (*model.AgentStatusResponse, error) {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return nil, fmt.Errorf("find container for agent %q: %w", agentName, err)
	}
	if c == nil {
		return nil, fmt.Errorf("agent %q: %w", agentName, ErrAgentNotFound)
	}

	// Inspect for health-check status (FindAgentContainer doesn't populate Health).
	detailed, err := s.dc.InspectContainer(ctx, c.ID)
	if err != nil {
		// Fall back to the list-level info if inspect fails.
		return &model.AgentStatusResponse{
			AgentName:     c.AgentName,
			ContainerName: c.Name,
			ContainerID:   c.ID,
			Status:        string(toStatus(c.Status)),
			Health:        "none",
			HostPort:      c.HostPort,
			Image:         c.Image,
			Upstream:      s.upstreamFor(c),
		}, nil
	}

	health := detailed.Health
	if health == "" {
		health = "none"
	}

	resp := &model.AgentStatusResponse{
		AgentName:     c.AgentName,
		ContainerName: detailed.Name,
		ContainerID:   detailed.ID,
		Status:        detailed.Status,
		Health:        health,
		HostPort:      detailed.HostPort,
		Image:         detailed.Image,
		Upstream:      s.upstreamFor(detailed),
	}
	if s.cfg.UpstreamProbe && resp.Upstream != nil {
		reachable := s.dialTCP(ctx, net.JoinHostPort(resp.Upstream.Host, strconv.Itoa(resp.Upstream.Port))) == nil
		resp.UpstreamReachable = &reachable
	}
	return resp, nil
}

// GetLogs returns the last `tail` lines of the agent's container output.
func (s *AgentService) GetLogs(ctx context.Context, name string, tail int) (string, error) {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return "", fmt.Errorf("find container for agent %q: %w", agentName, err)
	}
	if c == nil {
		return "", fmt.Errorf("agent %q: %w", agentName, ErrAgentNotFound)
	}
	return s.dc.ContainerLogs(ctx, c.ID, tail)
}

// Stop stops an agent's container.
func (s *AgentService) Stop(ctx context.Context, name string) error {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return fmt.Errorf("find container for agent %q: %w", agentName, err)
	}
	if c == nil {
		return fmt.Errorf("agent %q: %w", agentName, ErrAgentNotFound)
	}
	if err := s.dc.StopContainer(ctx, c.ID); err != nil {
		return fmt.Errorf("stop container %s: %w", c.ID, err)
	}
	return nil
}

// Restart restarts an agent's container.
func (s *AgentService) Restart(ctx context.Context, name string) error {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return fmt.Errorf("find container for agent %q: %w", agentName, err)
	}
	if c == nil {
		return fmt.Errorf("agent %q: %w", agentName, ErrAgentNotFound)
	}
	if err := s.dc.RestartContainer(ctx, c.ID); err != nil {
		return fmt.Errorf("restart container %s: %w", c.ID, err)
	}
	return nil
}

// Delete removes an agent's container. By default the per-agent data on disk
// is preserved so the agent becomes "archived" and can still be discovered via
// List(includeArchived=true). When purge is true, the container AND the
// on-disk data are removed (best-effort).
func (s *AgentService) Delete(ctx context.Context, name string, purge bool) error {
	agentName := naming.SanitizeName(name)
	c, err := s.dc.FindAgentContainer(ctx, agentName)
	if err != nil {
		return fmt.Errorf("find container for agent %q: %w", agentName, err)
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
		// Remove the entire per-agent directory (agents + sessions + skills).
		_ = storage.RemoveAll(filepath.Join(s.cfg.DataDir, agentName))
	}

	return nil
}

// toResponse maps a RuntimeContainer to an AgentResponse, filling the
// persistent path fields (YamlPath / SessionDir / SkillsDir) from cfg.DataDir
// and the CreatedAt timestamp from the Docker label. Unknown Docker statuses
// are normalized to model.StatusUnknown.
func (s *AgentService) toResponse(c *docker.RuntimeContainer) *model.AgentResponse {
	agentDir := filepath.Join(s.cfg.DataDir, c.AgentName, "agents")
	return &model.AgentResponse{
		AgentName:     c.AgentName,
		InstanceID:    c.InstanceID,
		ContainerID:   c.ID,
		ContainerName: c.Name,
		Status:        toStatus(c.Status),
		HostPort:      c.HostPort,
		CreatedAt:     c.CreatedAt,
		YamlPath:      filepath.Join(agentDir, "agents.yaml"),
		SessionDir:    filepath.Join(s.cfg.DataDir, c.AgentName, "sessions"),
		SkillsDir:     filepath.Join(s.cfg.DataDir, c.AgentName, "skills"),
		Upstream:      s.upstreamFor(c),
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

// toArchivedResponse builds an AgentResponse for an agent whose container is
// gone but whose on-disk data remains. It is used by Get/List when
// includeArchived is true. All container-derived fields are empty and status
// is "archived".
func (s *AgentService) toArchivedResponse(agentName string) *model.AgentResponse {
	return &model.AgentResponse{
		AgentName:  agentName,
		Status:     model.StatusArchived,
		YamlPath:   filepath.Join(s.cfg.DataDir, agentName, "agents", "agents.yaml"),
		SessionDir: filepath.Join(s.cfg.DataDir, agentName, "sessions"),
		SkillsDir:  filepath.Join(s.cfg.DataDir, agentName, "skills"),
	}
}
