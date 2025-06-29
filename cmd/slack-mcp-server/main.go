package main

import (
	"context"
	"flag"
	"log"
	"os"
	"strconv"

	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server"
)

var defaultSseHost = "127.0.0.1"
var defaultSsePort = 13080

func main() {
	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio or sse)")
	flag.StringVar(&transport, "transport", "stdio", "Transport type (stdio or sse)")
	flag.Parse()

	p := provider.New()

	s := server.NewMCPServer(
		p,
	)

	go func() {
		newUsersWatcher(p)()
		newChannelsWatcher(p)()
	}()

	switch transport {
	case "stdio":
		if err := s.ServeStdio(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "sse":
		host := os.Getenv("SLACK_MCP_HOST")
		if host == "" {
			host = defaultSseHost
		}
		port := os.Getenv("SLACK_MCP_PORT")
		if port == "" {
			port = strconv.Itoa(defaultSsePort)
		}

		sseServer := s.ServeSSE(":" + port)
		log.Printf("SSE server listening on " + host + ":" + port)
		if err := sseServer.Start(host + ":" + port); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	default:
		log.Fatalf("Invalid transport type: %s. Must be 'stdio' or 'sse'",
			transport,
		)
	}
}

func newUsersWatcher(p *provider.ApiProvider) func() {
	return func() {
		log.Println("Caching users collection...")

		if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
			log.Println("Demo credentials are set, skip.")
			return
		}

		err := p.RefreshUsers(context.Background())
		if err != nil {
			log.Fatalf("Error booting provider: %v", err)
		}

		log.Println("Users cached successfully.")
	}
}

func newChannelsWatcher(p *provider.ApiProvider) func() {
	return func() {
		log.Println("Caching channels collection...")

		if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
			log.Println("Demo credentials are set, skip.")
			return
		}

		err := p.RefreshChannels(context.Background())
		if err != nil {
			log.Fatalf("Error booting provider: %v", err)
		}

		log.Println("Channels cached successfully.")
	}
}
