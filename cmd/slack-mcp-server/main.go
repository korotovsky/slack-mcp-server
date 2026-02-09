package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/handler"
	"github.com/korotovsky/slack-mcp-server/pkg/oauth"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server"
	"github.com/mattn/go-isatty"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var defaultSseHost = "127.0.0.1"
var defaultSsePort = 13080

func main() {
	var transport string
	flag.StringVar(&transport, "t", "stdio", "Transport type (stdio, sse or http)")
	flag.StringVar(&transport, "transport", "stdio", "Transport type (stdio, sse or http)")
	flag.Parse()

	logger, err := newLogger(transport)
	if err != nil {
		panic(err)
	}
	defer logger.Sync()

	err = validateToolConfig(os.Getenv("SLACK_MCP_ADD_MESSAGE_TOOL"))
	if err != nil {
		logger.Fatal("error in SLACK_MCP_ADD_MESSAGE_TOOL",
			zap.String("context", "console"),
			zap.Error(err),
		)
	}

	// Check if OAuth mode is enabled
	oauthEnabled := os.Getenv("SLACK_MCP_OAUTH_ENABLED") == "true"

	var s *server.MCPServer
	var oauthHandler *server.OAuthHandler
	var oauthManager oauth.OAuthManager
	var p *provider.ApiProvider

	if oauthEnabled {
		// OAuth Mode
		logger.Info("OAuth mode enabled", zap.String("context", "console"))

		// Validate OAuth configuration
		clientID := os.Getenv("SLACK_MCP_OAUTH_CLIENT_ID")
		clientSecret := os.Getenv("SLACK_MCP_OAUTH_CLIENT_SECRET")
		redirectURI := os.Getenv("SLACK_MCP_OAUTH_REDIRECT_URI")

		if clientID == "" || clientSecret == "" || redirectURI == "" {
			logger.Fatal("OAuth enabled but missing required credentials",
				zap.String("context", "console"),
				zap.Bool("has_client_id", clientID != ""),
				zap.Bool("has_client_secret", clientSecret != ""),
				zap.Bool("has_redirect_uri", redirectURI != ""),
			)
		}

		// Create OAuth components
		tokenStorage := oauth.NewMemoryStorage()
		oauthManager = oauth.NewManager(clientID, clientSecret, redirectURI, tokenStorage)

		// Create OAuth handler for HTTP endpoints
		oauthHandler = server.NewOAuthHandler(oauthManager, logger)

		// Create handlers with OAuth support
		conversationsHandler := handler.NewConversationsHandlerWithOAuth(tokenStorage, logger)
		channelsHandler := handler.NewChannelsHandlerWithOAuth(tokenStorage, logger)

		// Create MCP server with OAuth middleware
		s = server.NewMCPServerWithOAuth(conversationsHandler, channelsHandler, oauthManager, logger)

		logger.Info("OAuth server initialized",
			zap.String("context", "console"),
			zap.String("redirect_uri", redirectURI),
		)
	} else {
		// Legacy Mode (existing code)
		logger.Info("Legacy mode enabled", zap.String("context", "console"))

		// Token resolution: env var → file store → local OAuth flow
		if err := resolveToken(transport, logger); err != nil {
			logger.Fatal("failed to resolve Slack token",
				zap.String("context", "console"),
				zap.Error(err),
			)
		}

		p = provider.New(transport, logger)
		s = server.NewMCPServer(p, logger)

		go func() {
			var once sync.Once

			newUsersWatcher(p, &once, logger)()
			newChannelsWatcher(p, &once, logger)()
		}()
	}

	switch transport {
	case "stdio":
		for {
			if ready, _ := p.IsReady(); ready {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if err := s.ServeStdio(); err != nil {
			logger.Fatal("Server error",
				zap.String("context", "console"),
				zap.Error(err),
			)
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

		addr := host + ":" + port

		if oauthEnabled && oauthHandler != nil {
			// OAuth mode: use combined handler with HTTP-level auth
			handler := s.ServeSSEWithOAuth(":"+port, oauthHandler, oauthManager)

			logger.Info("OAuth endpoints enabled",
				zap.String("context", "console"),
				zap.String("authorize_url", fmt.Sprintf("http://%s/oauth/authorize", addr)),
				zap.String("callback_url", fmt.Sprintf("http://%s/oauth/callback", addr)),
			)

			logger.Info(
				fmt.Sprintf("SSE server listening on %s/sse", addr),
				zap.String("context", "console"),
				zap.String("host", host),
				zap.String("port", port),
			)

			if err := http.ListenAndServe(addr, handler); err != nil {
				logger.Fatal("Server error",
					zap.String("context", "console"),
					zap.Error(err),
				)
			}
		} else {
			// Legacy mode
			handler := s.ServeSSE(":" + port)

			logger.Info(
				fmt.Sprintf("SSE server listening on %s/sse", addr),
				zap.String("context", "console"),
				zap.String("host", host),
				zap.String("port", port),
			)

			if ready, _ := p.IsReady(); !ready {
				logger.Info("Slack MCP Server is still warming up caches",
					zap.String("context", "console"),
				)
			}

			if err := http.ListenAndServe(addr, handler); err != nil {
				logger.Fatal("Server error",
					zap.String("context", "console"),
					zap.Error(err),
				)
			}
		}
	case "http":
		host := os.Getenv("SLACK_MCP_HOST")
		if host == "" {
			host = defaultSseHost
		}
		port := os.Getenv("SLACK_MCP_PORT")
		if port == "" {
			port = strconv.Itoa(defaultSsePort)
		}

		addr := host + ":" + port

		if oauthEnabled && oauthHandler != nil {
			// OAuth mode: use combined handler with HTTP-level auth
			handler := s.ServeHTTPWithOAuth(":"+port, oauthHandler, oauthManager)

			logger.Info("OAuth endpoints enabled",
				zap.String("context", "console"),
				zap.String("authorize_url", fmt.Sprintf("http://%s/oauth/authorize", addr)),
				zap.String("callback_url", fmt.Sprintf("http://%s/oauth/callback", addr)),
			)

			logger.Info(
				fmt.Sprintf("HTTP server listening on %s", addr),
				zap.String("context", "console"),
				zap.String("host", host),
				zap.String("port", port),
			)

			if err := http.ListenAndServe(addr, handler); err != nil {
				logger.Fatal("Server error",
					zap.String("context", "console"),
					zap.Error(err),
				)
			}
		} else {
			// Legacy mode
			handler := s.ServeHTTP(":" + port)

			logger.Info(
				fmt.Sprintf("HTTP server listening on %s", addr),
				zap.String("context", "console"),
				zap.String("host", host),
				zap.String("port", port),
			)

			if ready, _ := p.IsReady(); !ready {
				logger.Info("Slack MCP Server is still warming up caches",
					zap.String("context", "console"),
				)
			}

			if err := http.ListenAndServe(addr, handler); err != nil {
				logger.Fatal("Server error",
					zap.String("context", "console"),
					zap.Error(err),
				)
			}
		}
	default:
		logger.Fatal("Invalid transport type",
			zap.String("context", "console"),
			zap.String("transport", transport),
			zap.String("allowed", "stdio, sse, http"),
		)
	}
}

func newUsersWatcher(p *provider.ApiProvider, once *sync.Once, logger *zap.Logger) func() {
	return func() {
		logger.Info("Caching users collection...",
			zap.String("context", "console"),
		)

		if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
			logger.Info("Demo credentials are set, skip",
				zap.String("context", "console"),
			)
			return
		}

		err := p.RefreshUsers(context.Background())
		if err != nil {
			logger.Fatal("Error booting provider",
				zap.String("context", "console"),
				zap.Error(err),
			)
		}

		ready, _ := p.IsReady()
		if ready {
			once.Do(func() {
				logger.Info("Slack MCP Server is fully ready",
					zap.String("context", "console"),
				)
			})
		}
	}
}

func newChannelsWatcher(p *provider.ApiProvider, once *sync.Once, logger *zap.Logger) func() {
	return func() {
		logger.Info("Caching channels collection...",
			zap.String("context", "console"),
		)

		if os.Getenv("SLACK_MCP_XOXP_TOKEN") == "demo" || (os.Getenv("SLACK_MCP_XOXC_TOKEN") == "demo" && os.Getenv("SLACK_MCP_XOXD_TOKEN") == "demo") {
			logger.Info("Demo credentials are set, skip.",
				zap.String("context", "console"),
			)
			return
		}

		err := p.RefreshChannels(context.Background())
		if err != nil {
			logger.Fatal("Error booting provider",
				zap.String("context", "console"),
				zap.Error(err),
			)
		}

		ready, _ := p.IsReady()
		if ready {
			once.Do(func() {
				logger.Info("Slack MCP Server is fully ready.",
					zap.String("context", "console"),
				)
			})
		}
	}
}

func validateToolConfig(config string) error {
	if config == "" || config == "true" || config == "1" {
		return nil
	}

	items := strings.Split(config, ",")
	hasNegated := false
	hasPositive := false

	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "!") {
			hasNegated = true
		} else {
			hasPositive = true
		}
	}

	if hasNegated && hasPositive {
		return fmt.Errorf("cannot mix allowed and disallowed (! prefixed) channels")
	}

	return nil
}

func newLogger(transport string) (*zap.Logger, error) {
	atomicLevel := zap.NewAtomicLevelAt(zap.InfoLevel)
	if envLevel := os.Getenv("SLACK_MCP_LOG_LEVEL"); envLevel != "" {
		if err := atomicLevel.UnmarshalText([]byte(envLevel)); err != nil {
			fmt.Printf("Invalid log level '%s': %v, using 'info'\n", envLevel, err)
		}
	}

	useJSON := shouldUseJSONFormat()
	useColors := shouldUseColors() && !useJSON

	outputPath := "stdout"
	if transport == "stdio" {
		outputPath = "stderr"
	}

	var config zap.Config

	if useJSON {
		config = zap.Config{
			Level:            atomicLevel,
			Development:      false,
			Encoding:         "json",
			OutputPaths:      []string{outputPath},
			ErrorOutputPaths: []string{"stderr"},
			EncoderConfig: zapcore.EncoderConfig{
				TimeKey:       "timestamp",
				LevelKey:      "level",
				NameKey:       "logger",
				MessageKey:    "message",
				StacktraceKey: "stacktrace",
				EncodeLevel:   zapcore.LowercaseLevelEncoder,
				EncodeTime:    zapcore.RFC3339TimeEncoder,
				EncodeCaller:  zapcore.ShortCallerEncoder,
			},
		}
	} else {
		config = zap.Config{
			Level:            atomicLevel,
			Development:      true,
			Encoding:         "console",
			OutputPaths:      []string{outputPath},
			ErrorOutputPaths: []string{"stderr"},
			EncoderConfig: zapcore.EncoderConfig{
				TimeKey:          "timestamp",
				LevelKey:         "level",
				NameKey:          "logger",
				MessageKey:       "msg",
				StacktraceKey:    "stacktrace",
				EncodeLevel:      getConsoleLevelEncoder(useColors),
				EncodeTime:       zapcore.ISO8601TimeEncoder,
				EncodeCaller:     zapcore.ShortCallerEncoder,
				ConsoleSeparator: " | ",
			},
		}
	}

	logger, err := config.Build(zap.AddCaller())
	if err != nil {
		return nil, err
	}

	logger = logger.With(zap.String("app", "slack-mcp-server"))

	return logger, err
}

// shouldUseJSONFormat determines if JSON format should be used
func shouldUseJSONFormat() bool {
	if format := os.Getenv("SLACK_MCP_LOG_FORMAT"); format != "" {
		return strings.ToLower(format) == "json"
	}

	if env := os.Getenv("ENVIRONMENT"); env != "" {
		switch strings.ToLower(env) {
		case "production", "prod", "staging":
			return true
		case "development", "dev", "local":
			return false
		}
	}

	if os.Getenv("KUBERNETES_SERVICE_HOST") != "" ||
		os.Getenv("DOCKER_CONTAINER") != "" ||
		os.Getenv("container") != "" {
		return true
	}

	if !isatty.IsTerminal(os.Stdout.Fd()) {
		return true
	}

	return false
}

func shouldUseColors() bool {
	if colorEnv := os.Getenv("SLACK_MCP_LOG_COLOR"); colorEnv != "" {
		return colorEnv == "true" || colorEnv == "1"
	}

	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	if os.Getenv("FORCE_COLOR") != "" {
		return true
	}

	if env := os.Getenv("ENVIRONMENT"); env == "development" || env == "dev" {
		return isatty.IsTerminal(os.Stdout.Fd())
	}

	return isatty.IsTerminal(os.Stdout.Fd())
}

func getConsoleLevelEncoder(useColors bool) zapcore.LevelEncoder {
	if useColors {
		return zapcore.CapitalColorLevelEncoder
	}
	return zapcore.CapitalLevelEncoder
}

// resolveToken checks for a Slack token in order: env vars → file store → local OAuth flow.
// If a token is found or obtained, it sets SLACK_MCP_XOXP_TOKEN so provider.New() picks it up.
func resolveToken(transport string, logger *zap.Logger) error {
	// 1. Check env vars first (backward compatible)
	if os.Getenv("SLACK_MCP_XOXP_TOKEN") != "" ||
		os.Getenv("SLACK_MCP_XOXB_TOKEN") != "" ||
		os.Getenv("SLACK_MCP_XOXC_TOKEN") != "" {
		logger.Info("using token from environment variable",
			zap.String("context", "console"),
		)
		return nil
	}

	// 2. Check file store (~/.slack-mcp/token.json)
	fileStore := oauth.NewFileStorage("")
	token, err := fileStore.GetAnyToken()
	if err == nil && token.AccessToken != "" {
		logger.Info("using stored token from file",
			zap.String("context", "console"),
			zap.String("user_id", token.UserID),
			zap.String("team_id", token.TeamID),
		)
		os.Setenv("SLACK_MCP_XOXP_TOKEN", token.AccessToken)
		return nil
	}

	// 3. No stored token — trigger local OAuth flow
	// Only supported for stdio transport (interactive CLI use)
	if transport != "stdio" {
		return fmt.Errorf(
			"no Slack token found. Set SLACK_MCP_XOXP_TOKEN env var, " +
				"or run with stdio transport to use the local OAuth flow",
		)
	}

	clientID := os.Getenv("SLACK_MCP_OAUTH_CLIENT_ID")
	clientSecret := os.Getenv("SLACK_MCP_OAUTH_CLIENT_SECRET")
	if clientID == "" || clientSecret == "" {
		return fmt.Errorf(
			"no Slack token found and SLACK_MCP_OAUTH_CLIENT_ID / SLACK_MCP_OAUTH_CLIENT_SECRET not set. " +
				"Either set SLACK_MCP_XOXP_TOKEN or provide OAuth client credentials for the local login flow",
		)
	}

	logger.Info("no stored token found, starting local OAuth flow",
		zap.String("context", "console"),
	)

	token, err = oauth.LocalOAuthFlow(clientID, clientSecret, logger)
	if err != nil {
		return fmt.Errorf("local OAuth flow failed: %w", err)
	}

	// 4. Store the token for next time
	if err := fileStore.Store(token.UserID, token); err != nil {
		logger.Warn("failed to persist token to file (will need to re-auth next time)",
			zap.String("context", "console"),
			zap.Error(err),
		)
	} else {
		logger.Info("token stored for future use",
			zap.String("context", "console"),
			zap.String("path", "~/.slack-mcp/token.json"),
		)
	}

	// 5. Set the token so provider.New() picks it up
	os.Setenv("SLACK_MCP_XOXP_TOKEN", token.AccessToken)

	return nil
}
