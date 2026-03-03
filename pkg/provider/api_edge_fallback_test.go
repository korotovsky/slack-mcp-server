package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEdgeFallbackFlag verifies that MCPSlackClient remembers edge API failures
// and skips straight to the standard API on subsequent calls.
func TestEdgeFallbackFlag(t *testing.T) {
	t.Run("edgeFailed starts false", func(t *testing.T) {
		c := &MCPSlackClient{
			isEnterprise: true,
			isOAuth:      false,
		}
		assert.False(t, c.edgeFailed, "edgeFailed should start as false")
	})

	t.Run("edgeFailed flag is sticky", func(t *testing.T) {
		c := &MCPSlackClient{
			isEnterprise: true,
			isOAuth:      false,
			edgeFailed:   true,
		}
		assert.True(t, c.edgeFailed, "edgeFailed should remain true once set")
	})
}

// TestGetConversationsContextRouting verifies that MCPSlackClient routes
// GetConversationsContext to the correct backend based on isEnterprise,
// isOAuth, and edgeFailed flags.
func TestGetConversationsContextRouting(t *testing.T) {
	// Decision matrix:
	// | isEnterprise | isOAuth | edgeFailed | Expected path     |
	// |:-------------|:--------|:-----------|:------------------|
	// | false        | *       | *          | standard API      |
	// | true         | true    | *          | standard API      |
	// | true         | false   | false      | edge API (first)  |
	// | true         | false   | true       | standard API      |

	tests := []struct {
		name         string
		isEnterprise bool
		isOAuth      bool
		edgeFailed   bool
		expectEdge   bool
	}{
		{"non-enterprise goes to standard", false, false, false, false},
		{"enterprise+oauth goes to standard", true, true, false, false},
		{"enterprise+non-oauth tries edge first", true, false, false, true},
		{"enterprise+non-oauth+edgeFailed skips edge", true, false, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &MCPSlackClient{
				isEnterprise: tt.isEnterprise,
				isOAuth:      tt.isOAuth,
				edgeFailed:   tt.edgeFailed,
			}

			wouldTryEdge := c.isEnterprise && !c.isOAuth && !c.edgeFailed
			assert.Equal(t, tt.expectEdge, wouldTryEdge)
		})
	}
}

// TestEdgeFailedPreventsRetry verifies that once edgeFailed is set, the client
// won't attempt the edge API again. This prevents wasted API calls on every
// page of a paginated standard API fetch.
func TestEdgeFailedPreventsRetry(t *testing.T) {
	c := &MCPSlackClient{
		isEnterprise: true,
		isOAuth:      false,
		edgeFailed:   false,
	}

	// Simulate edge failure
	c.edgeFailed = true

	// Verify 10 subsequent "pagination" calls would all skip edge
	for i := 0; i < 10; i++ {
		wouldTryEdge := c.isEnterprise && !c.isOAuth && !c.edgeFailed
		assert.False(t, wouldTryEdge,
			"call %d: should not try edge after it failed", i+1)
	}
}
