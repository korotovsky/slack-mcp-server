package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// authKey is a custom context key for storing the auth token.
type authKey struct{}

// withAuthKey adds an auth key to the context.
func withAuthKey(ctx context.Context, auth string) context.Context {
	return context.WithValue(ctx, authKey{}, auth)
}

// Authenticate checks if the request is authenticated based on the provided context.
func validateToken(ctx context.Context, logger *zap.Logger) (bool, error) {
	// In pass-through auth mode the bearer token IS the Slack xoxp token; skip
	// API key validation so it is forwarded to the Slack client unchanged.
	if os.Getenv("SLACK_MCP_PASS_THROUGH_AUTH") == "true" {
		logger.Debug("Pass-through auth mode: skipping API key validation",
			zap.String("context", "http"),
		)
		return true, nil
	}

	// no configured token means no authentication
	keyA := os.Getenv("SLACK_MCP_API_KEY")
	if keyA == "" {
		keyA = os.Getenv("SLACK_MCP_SSE_API_KEY")
		if keyA != "" {
			logger.Warn("SLACK_MCP_SSE_API_KEY is deprecated, please use SLACK_MCP_API_KEY")
		}
	}

	if keyA == "" {
		logger.Debug("No SSE API key configured, skipping authentication",
			zap.String("context", "http"),
		)
		return true, nil
	}

	keyB, ok := ctx.Value(authKey{}).(string)
	if !ok {
		logger.Warn("Missing auth token in context",
			zap.String("context", "http"),
		)
		return false, fmt.Errorf("missing auth")
	}

	logger.Debug("Validating auth token",
		zap.String("context", "http"),
		zap.Bool("has_bearer_prefix", strings.HasPrefix(keyB, "Bearer ")),
	)

	if strings.HasPrefix(keyB, "Bearer ") {
		keyB = strings.TrimPrefix(keyB, "Bearer ")
	}

	if subtle.ConstantTimeCompare([]byte(keyA), []byte(keyB)) != 1 {
		logger.Warn("Invalid auth token provided",
			zap.String("context", "http"),
		)
		return false, fmt.Errorf("invalid auth token")
	}

	logger.Debug("Auth token validated successfully",
		zap.String("context", "http"),
	)
	return true, nil
}

// AuthFromRequest extracts the auth token from the request headers.
func AuthFromRequest(logger *zap.Logger) func(context.Context, *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		authHeader := r.Header.Get("Authorization")
		return withAuthKey(ctx, authHeader)
	}
}

// AuthTokenFromContext returns the raw bearer token stored in the context by
// AuthFromRequest, with the "Bearer " prefix stripped. Returns an empty string
// if no token is present in the context.
func AuthTokenFromContext(ctx context.Context) string {
	token, _ := ctx.Value(authKey{}).(string)
	return strings.TrimPrefix(token, "Bearer ")
}

// BuildMiddleware creates a middleware function that ensures authentication based on the provided transport type.
func BuildMiddleware(transport string, logger *zap.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logger.Debug("Auth middleware invoked",
				zap.String("context", "http"),
				zap.String("transport", transport),
				zap.String("tool", req.Params.Name),
			)

			if authenticated, err := IsAuthenticated(ctx, transport, logger); !authenticated {
				logger.Error("Authentication failed",
					zap.String("context", "http"),
					zap.String("transport", transport),
					zap.String("tool", req.Params.Name),
					zap.Error(err),
				)
				return nil, err
			}

			logger.Debug("Authentication successful",
				zap.String("context", "http"),
				zap.String("transport", transport),
				zap.String("tool", req.Params.Name),
			)

			return next(ctx, req)
		}
	}
}

// IsAuthenticated public api
func IsAuthenticated(ctx context.Context, transport string, logger *zap.Logger) (bool, error) {
	switch transport {
	case "stdio":
		return true, nil

	case "sse", "http":
		authenticated, err := validateToken(ctx, logger)

		if err != nil {
			logger.Error("HTTP/SSE authentication error",
				zap.String("context", "http"),
				zap.Error(err),
			)
			return false, fmt.Errorf("authentication error: %w", err)
		}

		if !authenticated {
			logger.Warn("HTTP/SSE unauthorized request",
				zap.String("context", "http"),
			)
			return false, fmt.Errorf("unauthorized request")
		}

		return true, nil

	default:
		logger.Error("Unknown transport type",
			zap.String("context", "http"),
			zap.String("transport", transport),
		)
		return false, fmt.Errorf("unknown transport type: %s", transport)
	}
}
