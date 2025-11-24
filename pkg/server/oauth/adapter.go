package oauth

import (
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
)

// StoreAdapter adapts oauth.Store to auth.TokenStore interface
type StoreAdapter struct {
	store Store
}

// NewStoreAdapter creates a new adapter
func NewStoreAdapter(store Store) auth.TokenStore {
	return &StoreAdapter{store: store}
}

// GetToken implements auth.TokenStore.GetToken
func (a *StoreAdapter) GetToken(accessToken string) (auth.TokenInfo, error) {
	tokenInfo, err := a.store.GetToken(accessToken)
	if err != nil || tokenInfo == nil {
		return nil, err
	}
	// tokenInfo (*TokenInfo) already implements the auth.TokenInfo interface
	return tokenInfo, nil
}
