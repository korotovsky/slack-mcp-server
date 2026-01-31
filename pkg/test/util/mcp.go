package util

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type MCPConfig struct {
	SSEKey             string
	MessageToolEnabled bool
	MessageToolMark    bool
	EnabledTools       []string // List of enabled tools (empty = all tools)
}

type MCPConnection struct {
	Host     string
	Port     int
	Shutdown func()
}

func SetupMCP(cfg MCPConfig) (*MCPConnection, error) {
	xoxp := os.Getenv("SLACK_MCP_XOXP_TOKEN")
	if xoxp == "" {
		return nil, fmt.Errorf("SLACK_MCP_XOXP_TOKEN not set")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not get free port: %w", err)
	}
	tcpAddr := ln.Addr().(*net.TCPAddr)
	ln.Close()

	host := "127.0.0.1"
	port := tcpAddr.Port

	ctx, cancel := context.WithCancel(context.Background())
	cwd, err := os.Getwd()
	if err != nil {
		log.Printf("warning: could not get cwd: %v", err)
	}

	cmd := exec.CommandContext(ctx,
		"go", "run", cwd+"/../../cmd/slack-mcp-server/main.go",
		"--transport", "sse",
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Env = append(os.Environ(),
		"SLACK_MCP_XOXP_TOKEN="+xoxp,
		"SLACK_MCP_HOST="+host,
		"SLACK_MCP_PORT="+strconv.Itoa(port),
		"SLACK_MCP_ADD_MESSAGE_TOOL=true",
		"SLACK_MCP_API_KEY="+cfg.SSEKey,
		"SLACK_MCP_USERS_CACHE=/tmp/users_cache.json",
		"SLACK_MCP_CHANNELS_CACHE=/tmp/channels_cache_v3.json",
	)

	if len(cfg.EnabledTools) > 0 {
		cmd.Env = append(cmd.Env, "SLACK_MCP_ENABLED_TOOLS="+strings.Join(cfg.EnabledTools, ","))
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to get stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("failed to start MCP server: %w", err)
	}

	ready := make(chan struct{})
	done := make(chan struct{})

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Println(line)
			if strings.Contains(line, "Slack MCP Server is fully ready") {
				select {
				case <-ready:
					// already closed, ignore
				default:
					close(ready)
				}
			}
		}
	}()

	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(os.Stderr, line)
		}
	}()

	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	const bootTimeout = 30 * time.Second
	select {
	case <-ready:
		// ready to go
	case <-done:
		cancel()
		return nil, fmt.Errorf("MCP server exited before becoming ready")
	case <-time.After(bootTimeout):
		cancel()
		return nil, fmt.Errorf("timeout (%s) waiting for Slack MCP server to be ready", bootTimeout)
	}

	return &MCPConnection{
		Host: host,
		Port: port,
		Shutdown: func() {
			// Send SIGTERM to the process group
			if cmd.Process != nil {
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			}
			// Cancel context and wait for exit
			cancel()
			<-done
		},
	}, nil
}
