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

// TokenStore interface for OAuth2 token lookups
type TokenStore interface {
	GetToken(accessToken string) (TokenInfo, error)
}

// TokenInfo represents stored token information
type TokenInfo interface {
	GetAccessToken() string
	GetSlackToken() string
	GetUserID() string
	GetTeamID() string
}

var globalTokenStore TokenStore

// SetTokenStore sets the global token store for OAuth2 support
func SetTokenStore(store TokenStore) {
	globalTokenStore = store
}

// authKey is a custom context key for storing the auth token.
type authKey struct{}

// slackTokenKey is a custom context key for storing the per-request Slack OAuth token.
type slackTokenKey struct{}

// withAuthKey adds an auth key to the context.
func withAuthKey(ctx context.Context, auth string) context.Context {
	return context.WithValue(ctx, authKey{}, auth)
}

// WithSlackToken adds a Slack OAuth token to the context.
func WithSlackToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, slackTokenKey{}, token)
}

// GetSlackToken retrieves the Slack OAuth token from the context.
func GetSlackToken(ctx context.Context) (string, bool) {
	token, ok := ctx.Value(slackTokenKey{}).(string)
	return token, ok
}

// Authenticate checks if the request is authenticated based on the provided context.
func validateToken(ctx context.Context, logger *zap.Logger) (bool, error) {
	// If a per-request Slack OAuth token is present, skip API key validation
	if slackToken, ok := GetSlackToken(ctx); ok && slackToken != "" {
		logger.Debug("Per-request Slack OAuth token present, skipping API key validation",
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

// isSlackToken checks if the given token is a Slack OAuth token based on its prefix.
func isSlackToken(token string) bool {
	// Remove "Bearer " prefix if present
	token = strings.TrimPrefix(token, "Bearer ")
	token = strings.TrimSpace(token)

	// Check for common Slack token prefixes
	return strings.HasPrefix(token, "xoxp-") ||
		strings.HasPrefix(token, "xoxc-") ||
		strings.HasPrefix(token, "xoxb-") ||
		strings.HasPrefix(token, "xoxd-")
}

// AuthFromRequest extracts the auth token from the request headers.
// It differentiates between Slack OAuth tokens (xoxp-, xoxc-, xoxb-, xoxd-),
// OAuth2 MCP tokens, and API keys, storing them in separate context values.
func AuthFromRequest(logger *zap.Logger) func(context.Context, *http.Request) context.Context {
	return func(ctx context.Context, r *http.Request) context.Context {
		authHeader := r.Header.Get("Authorization")

		if authHeader != "" {
			token := strings.TrimPrefix(authHeader, "Bearer ")
			token = strings.TrimSpace(token)

			if isSlackToken(token) {
				logger.Debug("Detected Slack OAuth token in Authorization header",
					zap.String("context", "http"),
					zap.String("prefix", token[:5]+"..."),
				)
				// Store as Slack token
				ctx = WithSlackToken(ctx, token)
			} else if globalTokenStore != nil {
				// Check if it's an OAuth2 MCP token
				tokenInfo, err := globalTokenStore.GetToken(token)
				if err == nil && tokenInfo != nil {
					logger.Debug("Detected OAuth2 MCP token in Authorization header",
						zap.String("context", "http"),
						zap.String("user_id", tokenInfo.GetUserID()),
						zap.String("team_id", tokenInfo.GetTeamID()),
					)
					// If the token has a Slack token, use it
					if tokenInfo.GetSlackToken() != "" {
						ctx = WithSlackToken(ctx, tokenInfo.GetSlackToken())
					}
					// Token is valid, mark as authenticated
					ctx = withAuthKey(ctx, authHeader)
				} else {
					logger.Debug("Token not found in OAuth2 store, treating as API key",
						zap.String("context", "http"),
					)
					// Not an OAuth2 token, store as API key for validation
					ctx = withAuthKey(ctx, authHeader)
				}
			} else {
				logger.Debug("Detected API key in Authorization header",
					zap.String("context", "http"),
				)
				// Store as API key for validation
				ctx = withAuthKey(ctx, authHeader)
			}
		}

		return ctx
	}
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
