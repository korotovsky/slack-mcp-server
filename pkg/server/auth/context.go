package auth

import "context"

type userContextKey struct{}
type userTokenKey struct{}

// UserContext holds authenticated user information
type UserContext struct {
	UserID      string
	TeamID      string
	AccessToken string // User token (xoxp-...) for per-request client creation
	BotToken    string // Bot token (xoxb-...) if available - for posting as bot
	BotUserID   string // Bot user ID if available
}

// WithUserContext adds user context to the context
func WithUserContext(ctx context.Context, user *UserContext) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

// FromContext extracts user context from the context
func FromContext(ctx context.Context) (*UserContext, bool) {
	user, ok := ctx.Value(userContextKey{}).(*UserContext)
	return user, ok
}


