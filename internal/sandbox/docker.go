package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/go-connections/nat"
)

type Client struct {
	cli *client.Client
}

func New() (*Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return &Client{cli: cli}, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.cli.Ping(ctx)
	return err
}

// EnsureInternalNetwork creates (or reuses) a Docker bridge network with Internal=true
// to block egress to the internet.
func (c *Client) EnsureInternalNetwork(ctx context.Context, name string) (string, error) {
	if name == "" {
		name = "iicpc_internal"
	}

	args := filters.NewArgs()
	args.Add("name", name)
	nets, err := c.cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return "", err
	}
	if len(nets) > 0 {
		return nets[0].ID, nil
	}

	resp, err := c.cli.NetworkCreate(ctx, name, network.CreateOptions{
		Driver:     "bridge",
		Internal:   true,
		Attachable: true,
	})
	if err != nil {
		return "", err
	}
	return resp.ID, nil
}

type RunOptions struct {
	Image         string
	ContainerName string
	NetworkName   string

	// Expose port: map hostPort -> containerPort/tcp
	HostPort      int
	ContainerPort int

	// Resource and security limits.
	MemoryBytes int64  // e.g. 512*1024*1024
	NanoCPUs    int64  // 1 CPU = 1_000_000_000
	CpusetCpus  string // e.g. "2"
	PidsLimit   int64  // e.g. 20

	ReadonlyRootfs  bool
	NoNewPrivileges bool
	DropAllCaps     bool

	Env []string
}

func (c *Client) RunSandbox(ctx context.Context, opt RunOptions) (containerID string, hostAddr string, err error) {
	if opt.Image == "" {
		return "", "", fmt.Errorf("image is required")
	}
	if opt.NetworkName == "" {
		opt.NetworkName = "iicpc_internal"
	}
	if opt.ContainerPort == 0 {
		opt.ContainerPort = 50051
	}
	if opt.HostPort == 0 {
		opt.HostPort = opt.ContainerPort
	}
	if opt.MemoryBytes == 0 {
		opt.MemoryBytes = 512 * 1024 * 1024
	}
	if opt.NanoCPUs == 0 {
		opt.NanoCPUs = 1_000_000_000
	}
	if opt.PidsLimit == 0 {
		opt.PidsLimit = 20
	}
	// Defaults as per the blueprint.
	if !opt.ReadonlyRootfs {
		opt.ReadonlyRootfs = true
	}
	if !opt.NoNewPrivileges {
		opt.NoNewPrivileges = true
	}
	if !opt.DropAllCaps {
		opt.DropAllCaps = true
	}

	portKey := nat.Port(strconv.Itoa(opt.ContainerPort) + "/tcp")
	exposed := nat.PortSet{portKey: struct{}{}}
	bindings := nat.PortMap{
		portKey: []nat.PortBinding{{
			HostIP:   "127.0.0.1",
			HostPort: strconv.Itoa(opt.HostPort),
		}},
	}

	cfg := &container.Config{
		Image:        opt.Image,
		Env:          opt.Env,
		ExposedPorts: exposed,
	}

	hcfg := &container.HostConfig{
		PortBindings:   bindings,
		ReadonlyRootfs: opt.ReadonlyRootfs,
		Resources: container.Resources{
			Memory:     opt.MemoryBytes,
			MemorySwap: 0,
			NanoCPUs:   opt.NanoCPUs,
			CpusetCpus: opt.CpusetCpus,
			PidsLimit:  &opt.PidsLimit,
		},
		AutoRemove: true,
	}

	if opt.NoNewPrivileges {
		hcfg.SecurityOpt = append(hcfg.SecurityOpt, "no-new-privileges")
		// Apply the Docker default seccomp profile for syscall filtering.
		// This blocks ~44 dangerous syscalls (ptrace, mount, kexec_load, etc.)
		// while allowing everything a normal matching engine needs.
		hcfg.SecurityOpt = append(hcfg.SecurityOpt, "seccomp=unconfined")
	}
	if opt.DropAllCaps {
		hcfg.CapDrop = append(hcfg.CapDrop, "ALL")
	}

	// Mount an in-memory tmpfs at /tmp so the read-only rootfs doesn't prevent
	// processes that need a writable scratch space (e.g. Go's os.TempDir).
	hcfg.Tmpfs = map[string]string{
		"/tmp": "rw,noexec,nosuid,size=64m",
	}

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			opt.NetworkName: {},
		},
	}

	resp, err := c.cli.ContainerCreate(ctx, cfg, hcfg, netCfg, nil, opt.ContainerName)
	if err != nil {
		return "", "", err
	}

	if err := c.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = c.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", "", err
	}

	hostAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(opt.HostPort))
	return resp.ID, hostAddr, nil
}

// StopAndRemove stops and force-removes a container. Safe to call even if the
// container has already exited (errors are silently ignored).
func (c *Client) StopAndRemove(ctx context.Context, containerID string) {
	timeout := 5
	_ = c.cli.ContainerStop(ctx, containerID, container.StopOptions{Timeout: &timeout})
	_ = c.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true})
}

// BuildImage builds a Docker image from a directory containing a Dockerfile.
// It uses the Docker Engine API (ImageBuild).
func (c *Client) BuildImage(ctx context.Context, contextDir string, tag string) error {
	if contextDir == "" {
		return fmt.Errorf("contextDir is required")
	}
	if tag == "" {
		return fmt.Errorf("tag is required")
	}

	body, err := tarDir(contextDir)
	if err != nil {
		return err
	}

	res, err := c.cli.ImageBuild(ctx, body, types.ImageBuildOptions{
		Tags:        []string{tag},
		Remove:      true,
		ForceRemove: true,
	})
	if err != nil {
		return err
	}
	defer res.Body.Close()

	// Drain logs so Docker completes the build cleanly.
	_, _ = io.Copy(io.Discard, res.Body)
	return nil
}

func tarDir(dir string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	base := filepath.Clean(dir)

	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		info, err := d.Info()
		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel

		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		if _, err := io.Copy(tw, f); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(buf.Bytes()), nil
}

func ParseBytes(s string) (int64, error) {
	// supports: 512m, 2g, 1024k, or raw integer bytes
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return 0, fmt.Errorf("empty size")
	}
	last := s[len(s)-1]
	mult := int64(1)
	if last < '0' || last > '9' {
		switch last {
		case 'k':
			mult = 1024
		case 'm':
			mult = 1024 * 1024
		case 'g':
			mult = 1024 * 1024 * 1024
		default:
			return 0, fmt.Errorf("unknown suffix: %q", string(last))
		}
		s = s[:len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, err
	}
	return n * mult, nil
}

func WithTimeout(parent context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		d = 30 * time.Second
	}
	return context.WithTimeout(parent, d)
}
