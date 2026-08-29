// Package docker provides a thin wrapper around the official Docker Engine SDK
// used by agent-deployer to manage the lifecycle of agent-runtime containers.
package docker

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-connections/nat"

	"github.com/zerone-agent/agent-deployer/internal/model"
)

// Docker labels applied to every managed container. They are used both for
// filtering (listing / finding containers) and for bookkeeping.
const (
	LabelManaged    = "agent-deployer/managed"
	LabelAgentName  = "agent-deployer/agent.name"
	LabelInstanceID = "agent-deployer/agent.instance-id"
	LabelCreatedAt  = "agent-deployer/agent.created-at"
)

// Client wraps the official Docker SDK client.
type Client struct {
	cli *client.Client
}

// CreateOpts groups all the parameters required to create and start an
// open-agent-runtime container.
type CreateOpts struct {
	AgentName            string
	InstanceID           string
	ContainerName        string
	Image                string
	RuntimeContainerPort int
	AgentDir             string
	SessionDir           string
	Agent                model.AgentDefinition
	Provider             model.ProviderConfig
	RuntimeToken         string // injected as ZERONE_AGENT_HTTP_API_KEY; supplied by caller
	MemoryBytes          int64  // 0 = unlimited
	NanoCPUs             int64  // 0 = unlimited
}

// NewClient returns a Docker client configured from the environment with
// automatic API-version negotiation.
func NewClient() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}
	return &Client{cli: cli}, nil
}

// Close releases the underlying Docker client resources.
func (c *Client) Close() error {
	if c.cli == nil {
		return nil
	}
	return c.cli.Close()
}

// Ping checks that the Docker daemon is reachable and returns its response.
func (c *Client) Ping(ctx context.Context) (types.Ping, error) {
	return c.cli.Ping(ctx)
}

// RuntimeContainer is the docker-package projection of a managed agent-runtime
// container. It isolates callers from the Docker SDK types.
type RuntimeContainer struct {
	ID         string
	Name       string // container name without leading "/"
	AgentName  string // from label agent-deployer/agent.name
	InstanceID string // from label agent-deployer/agent.instance-id
	Status     string // raw Docker state: "running", "exited", "created", etc.
	Health     string // Docker health: "starting", "healthy", "unhealthy", "" when no healthcheck
	HostPort   int    // first public port binding, 0 if none
	Image      string
	CreatedAt  string // RFC3339 UTC from label agent-deployer/agent.created-at; "" if missing
}

// toRuntimeContainer maps a Docker SDK container to the RuntimeContainer DTO.
func toRuntimeContainer(c types.Container) RuntimeContainer {
	name := ""
	if len(c.Names) > 0 {
		name = strings.TrimPrefix(c.Names[0], "/")
	}
	rc := RuntimeContainer{
		ID:         c.ID,
		Name:       name,
		AgentName:  c.Labels[LabelAgentName],
		InstanceID: c.Labels[LabelInstanceID],
		Status:     c.State,
		Image:      c.Image,
		CreatedAt:  c.Labels[LabelCreatedAt],
	}
	for _, p := range c.Ports {
		if p.PublicPort != 0 {
			rc.HostPort = int(p.PublicPort)
			break
		}
	}
	return rc
}

// FindAgentContainer returns the managed container for the given agent name,
// or (nil, nil) if no such container exists.
func (c *Client) FindAgentContainer(ctx context.Context, agentName string) (*RuntimeContainer, error) {
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s=true", LabelManaged)),
			filters.Arg("label", fmt.Sprintf("%s=%s", LabelAgentName, agentName)),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}
	if len(containers) == 0 {
		return nil, nil
	}
	rc := toRuntimeContainer(containers[0])
	return &rc, nil
}

// ListManagedContainers returns every container managed by agent-deployer.
func (c *Client) ListManagedContainers(ctx context.Context) ([]RuntimeContainer, error) {
	containers, err := c.cli.ContainerList(ctx, container.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", fmt.Sprintf("%s=true", LabelManaged)),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list managed containers: %w", err)
	}
	result := make([]RuntimeContainer, len(containers))
	for i, cc := range containers {
		result[i] = toRuntimeContainer(cc)
	}
	return result, nil
}

// InspectContainer returns detailed status of a container by ID, including
// the Docker health-check result. Use this when you need the Health field
// (FindAgentContainer / ListManagedContainers do not populate it).
func (c *Client) InspectContainer(ctx context.Context, containerID string) (*RuntimeContainer, error) {
	info, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("inspect container %s: %w", containerID, err)
	}

	rc := RuntimeContainer{
		ID:       info.ID,
		Name:     strings.TrimPrefix(info.Name, "/"),
		Status:   info.State.Status,
		Image:    info.Config.Image,
		HostPort: extractHostPort(info, c),
	}

	// Health comes from the inspect path, not from ContainerList
	if info.State.Health != nil {
		rc.Health = info.State.Health.Status
	}

	if info.Config.Labels != nil {
		rc.AgentName = info.Config.Labels[LabelAgentName]
		rc.InstanceID = info.Config.Labels[LabelInstanceID]
		rc.CreatedAt = info.Config.Labels[LabelCreatedAt]
	}

	return &rc, nil
}

// extractHostPort pulls the first public port binding from an inspect result.
func extractHostPort(info types.ContainerJSON, c *Client) int {
	if info.NetworkSettings == nil || info.NetworkSettings.Ports == nil {
		return 0
	}
	for _, bindings := range info.NetworkSettings.Ports {
		for _, b := range bindings {
			if b.HostPort != "" {
				if p, err := strconv.Atoi(b.HostPort); err == nil {
					return p
				}
			}
		}
	}
	return 0
}

// CreateAgentContainer creates and starts an agent-runtime container and
// returns the container ID together with the dynamically assigned host port.
func (c *Client) CreateAgentContainer(ctx context.Context, opts CreateOpts) (string, int, error) {
	portStr := fmt.Sprintf("%d/tcp", opts.RuntimeContainerPort)
	port := nat.Port(portStr)

	env := buildEnvVars(opts.RuntimeToken)

	labels := map[string]string{
		LabelManaged:    "true",
		LabelAgentName:  opts.AgentName,
		LabelInstanceID: opts.InstanceID,
		LabelCreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	containerCfg := &container.Config{
		Image: opts.Image,
		Env:   env,
		ExposedPorts: nat.PortSet{
			port: struct{}{},
		},
		Labels: labels,
	}

	hostMounts := []mount.Mount{
		{Type: mount.TypeBind, Source: opts.AgentDir, Target: "/app/config"},
		{Type: mount.TypeBind, Source: opts.SessionDir, Target: "/root/.agents"},
	}

	hostCfg := &container.HostConfig{
		PortBindings: nat.PortMap{
			port: []nat.PortBinding{{HostIP: "0.0.0.0", HostPort: ""}},
		},
		Mounts: hostMounts,
		RestartPolicy: container.RestartPolicy{
			Name: container.RestartPolicyUnlessStopped,
		},
		Resources: container.Resources{
			Memory:   opts.MemoryBytes,
			NanoCPUs: opts.NanoCPUs,
		},
	}

	networkCfg := &network.NetworkingConfig{}

	createResp, err := c.cli.ContainerCreate(ctx, containerCfg, hostCfg, networkCfg, nil, opts.ContainerName)
	if err != nil {
		return "", 0, fmt.Errorf("create container: %w", err)
	}

	containerID := createResp.ID

	if err := c.cli.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		// Best-effort cleanup: remove the half-created container so a retry
		// does not collide with an existing-but-stopped container.
		_ = c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
		return "", 0, fmt.Errorf("start container: %w", err)
	}

	inspectResp, err := c.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return containerID, 0, fmt.Errorf("inspect container: %w", err)
	}

	bindings, ok := inspectResp.NetworkSettings.Ports[port]
	if !ok || len(bindings) == 0 {
		return containerID, 0, fmt.Errorf("no host binding for port %s on container %s", portStr, containerID)
	}
	hostPort, err := strconv.Atoi(bindings[0].HostPort)
	if err != nil {
		return containerID, 0, fmt.Errorf("parse host port %q: %w", bindings[0].HostPort, err)
	}

	return containerID, hostPort, nil
}

// StopContainer stops the container with the given ID.
func (c *Client) StopContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerStop(ctx, id, container.StopOptions{}); err != nil {
		return fmt.Errorf("stop container %s: %w", id, err)
	}
	return nil
}

// RestartContainer restarts the container with the given ID.
func (c *Client) RestartContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerRestart(ctx, id, container.StopOptions{}); err != nil {
		return fmt.Errorf("restart container %s: %w", id, err)
	}
	return nil
}

// RemoveContainer force-removes the container with the given ID.
func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	if err := c.cli.ContainerRemove(ctx, id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove container %s: %w", id, err)
	}
	return nil
}

// ContainerLogs returns the last `tail` lines of container output (stdout +
// stderr combined). The Docker multiplexed stream is demuxed into a single
// string.
func (c *Client) ContainerLogs(ctx context.Context, id string, tail int) (string, error) {
	reader, err := c.cli.ContainerLogs(ctx, id, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Tail:       strconv.Itoa(tail),
	})
	if err != nil {
		return "", fmt.Errorf("fetch logs for %s: %w", id, err)
	}
	defer reader.Close()

	var stdout, stderr strings.Builder
	if _, err := stdcopy.StdCopy(&stdout, &stderr, reader); err != nil {
		return "", fmt.Errorf("read logs for %s: %w", id, err)
	}

	// Merge stdout and stderr into a single output.
	if stderr.Len() > 0 {
		if stdout.Len() > 0 {
			return stdout.String() + "\n" + stderr.String(), nil
		}
		return stderr.String(), nil
	}
	return stdout.String(), nil
}

// buildEnvVars constructs the environment variables for the agent-runtime
// process. Provider credentials (apiKey/baseURL/apiType) and the model are
// written into the runtime agents.yaml by the storage layer instead — the
// only env injected here is the runtime's own HTTP API auth token.
func buildEnvVars(runtimeToken string) []string {
	var env []string
	if runtimeToken != "" {
		env = append(env, "ZERONE_AGENT_HTTP_API_KEY="+runtimeToken)
	}
	return env
}

// CopySkillsToContainer copies the specified skill directories from sourceDir
// into the container at /workdir/.agents/skills/ using Docker's CopyToContainer
// API. Only the skills listed in skillNames are copied — stale cached skills
// from previous deployments are excluded.
// The container must already exist (running or stopped).
// An empty skillNames list is a no-op.
func (c *Client) CopySkillsToContainer(ctx context.Context, containerID, sourceDir string, skillNames []string) error {
	if len(skillNames) == 0 {
		return nil
	}

	pr, pw := io.Pipe()
	go func() {
		err := tarSkillDirs(sourceDir, ".agents/skills", skillNames, pw)
		pw.CloseWithError(err)
	}()

	err := c.cli.CopyToContainer(ctx, containerID, "/workdir/", pr, container.CopyToContainerOptions{})
	if err != nil {
		return fmt.Errorf("copy skills to container %s: %w", containerID, err)
	}
	return nil
}

// tarSkillDirs writes a tar archive containing only the named immediate
// subdirectories of sourceDir. Entries are prefixed with prefix and relative
// to sourceDir. Directories that don't exist on disk are silently skipped.
func tarSkillDirs(sourceDir, prefix string, skillNames []string, w io.Writer) error {
	tw := tar.NewWriter(w)
	defer tw.Close()

	for _, name := range skillNames {
		skillPath := filepath.Join(sourceDir, name)
		info, err := os.Stat(skillPath)
		if err != nil || !info.IsDir() {
			continue
		}

		err = filepath.Walk(skillPath, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			relPath, err := filepath.Rel(sourceDir, path)
			if err != nil {
				return err
			}
			if relPath == "." {
				return nil
			}

			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return fmt.Errorf("create tar header for %q: %w", relPath, err)
			}
			if prefix != "" {
				header.Name = filepath.ToSlash(filepath.Join(prefix, relPath))
			} else {
				header.Name = filepath.ToSlash(relPath)
			}

			if err := tw.WriteHeader(header); err != nil {
				return fmt.Errorf("write tar header for %q: %w", header.Name, err)
			}

			if !info.Mode().IsRegular() {
				return nil
			}

			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()

			_, err = io.Copy(tw, f)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
