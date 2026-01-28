package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/korotovsky/slack-mcp-server/pkg/oauth"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

// OAuthMiddleware validates OAuth tokens and injects user context
func OAuthMiddleware(oauthMgr oauth.OAuthManager, logger *zap.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			// Extract token from context
			token, ok := ctx.Value(authKey{}).(string)
			if !ok {
				logger.Warn("Missing auth token in OAuth mode")
				return nil, fmt.Errorf("missing authentication token")
			}

			// Remove Bearer prefix if present
			token = strings.TrimPrefix(token, "Bearer ")

			// Validate token
			tokenInfo, err := oauthMgr.ValidateToken(token)
			if err != nil {
				logger.Warn("Invalid token", zap.Error(err))
				return nil, fmt.Errorf("invalid authentication token: %w", err)
			}

			// Get full token response to access bot token if available
			storedToken, err := oauthMgr.GetStoredToken(tokenInfo.UserID)
			if err != nil {
				logger.Warn("Failed to retrieve stored token", zap.Error(err))
				// Fallback: use validated token without bot token
				storedToken = &oauth.TokenResponse{
					AccessToken: token,
					UserID:      tokenInfo.UserID,
					TeamID:      tokenInfo.TeamID,
				}
			}

			userCtx := &UserContext{
				UserID:      tokenInfo.UserID,
				TeamID:      tokenInfo.TeamID,
				AccessToken: token,                  // User token for per-request client
				BotToken:    storedToken.BotToken,   // Bot token if available
				BotUserID:   storedToken.BotUserID,  // Bot user ID if available
				URL:         tokenInfo.URL,          // Workspace URL for API calls
			}

			// Inject user context
			ctx = WithUserContext(ctx, userCtx)

			logger.Debug("Authenticated user",
				zap.String("userID", userCtx.UserID),
				zap.String("teamID", userCtx.TeamID),
			)

			return next(ctx, req)
		}
	}
}
