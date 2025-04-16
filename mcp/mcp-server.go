package main

import (
	"flag"
	"github.com/korotovsky/slack-mcp-server/internal/provider"
	"github.com/korotovsky/slack-mcp-server/internal/server"
	"log"
	"os"
	"strconv"
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
		log.Println("Booting provider...")

		_, err := p.Provide()
		if err != nil {
			log.Fatalf("Error booting provider: %v", err)
		}

		log.Println("Provider booted successfully.")
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
