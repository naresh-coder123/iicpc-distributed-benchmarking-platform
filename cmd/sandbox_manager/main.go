package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/iicpc/platform/internal/sandbox"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	switch cmd {
	case "net":
		netCmd(os.Args[2:])
	case "build":
		buildCmd(os.Args[2:])
	case "run":
		runCmd(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println("sandbox_manager commands:")
	fmt.Println("  net   --name iicpc_internal")
	fmt.Println("  build --context ./deploy/mock_matcher_go --tag iicpc/mock-matcher:latest")
	fmt.Println("  run   --image iicpc/mock-matcher:latest --network iicpc_internal --host-port 50051 --container-port 50051 --mem 512m --cpus 1 --cpuset 2 --pids 20")
}

func mustClient() *sandbox.Client {
	c, err := sandbox.New()
	if err != nil {
		fmt.Printf("docker client error: %v\n", err)
		os.Exit(1)
	}
	return c
}

func netCmd(args []string) {
	fs := flag.NewFlagSet("net", flag.ExitOnError)
	name := fs.String("name", "iicpc_internal", "network name")
	timeout := fs.Duration("timeout", 10*time.Second, "timeout")
	_ = fs.Parse(args)

	ctx, cancel := sandbox.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c := mustClient()
	if err := c.Ping(ctx); err != nil {
		fmt.Printf("docker daemon not reachable: %v\nStart Docker Desktop (Linux engine), then retry.\n", err)
		os.Exit(1)
	}
	id, err := c.EnsureInternalNetwork(ctx, *name)
	if err != nil {
		fmt.Printf("ensure network error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("network ready: %s (%s)\n", *name, id)
}

func buildCmd(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	contextDir := fs.String("context", "", "build context directory (must contain Dockerfile)")
	tag := fs.String("tag", "", "image tag")
	timeout := fs.Duration("timeout", 10*time.Minute, "timeout")
	_ = fs.Parse(args)

	ctx, cancel := sandbox.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c := mustClient()
	if err := c.Ping(ctx); err != nil {
		fmt.Printf("docker daemon not reachable: %v\nStart Docker Desktop (Linux engine), then retry.\n", err)
		os.Exit(1)
	}
	if err := c.BuildImage(ctx, *contextDir, *tag); err != nil {
		fmt.Printf("build error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("image built: %s\n", *tag)
}

func runCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	image := fs.String("image", "", "image tag to run")
	networkName := fs.String("network", "iicpc_internal", "network name (Internal=true recommended)")
	name := fs.String("name", "", "container name (optional)")
	hostPort := fs.Int("host-port", 50051, "host port to bind on 127.0.0.1")
	containerPort := fs.Int("container-port", 50051, "container port to expose")
	memStr := fs.String("mem", "512m", "memory limit (e.g., 512m, 2g)")
	cpus := fs.Int("cpus", 1, "CPU limit (integer cores, converted to NanoCPUs)")
	cpuset := fs.String("cpuset", "", "cpu set (e.g., 2 or 2-3)")
	pids := fs.Int64("pids", 20, "pids limit")
	timeout := fs.Duration("timeout", 30*time.Second, "timeout")
	_ = fs.Parse(args)

	memBytes, err := sandbox.ParseBytes(*memStr)
	if err != nil {
		fmt.Printf("bad --mem: %v\n", err)
		os.Exit(2)
	}

	ctx, cancel := sandbox.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c := mustClient()
	if err := c.Ping(ctx); err != nil {
		fmt.Printf("docker daemon not reachable: %v\nStart Docker Desktop (Linux engine), then retry.\n", err)
		os.Exit(1)
	}

	_, err = c.EnsureInternalNetwork(ctx, *networkName)
	if err != nil {
		fmt.Printf("ensure network error: %v\n", err)
		os.Exit(1)
	}

	opt := sandbox.RunOptions{
		Image:           *image,
		ContainerName:   *name,
		NetworkName:     *networkName,
		HostPort:        *hostPort,
		ContainerPort:   *containerPort,
		MemoryBytes:     memBytes,
		NanoCPUs:        int64(*cpus) * 1_000_000_000,
		CpusetCpus:      *cpuset,
		PidsLimit:       *pids,
		ReadonlyRootfs:  true,
		NoNewPrivileges: true,
		DropAllCaps:     true,
	}

	id, addr, err := c.RunSandbox(ctx, opt)
	if err != nil {
		fmt.Printf("run error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("sandbox running: id=%s host_addr=%s\n", id, addr)
}
