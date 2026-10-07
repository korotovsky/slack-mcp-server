package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnitInitialAPIURL(t *testing.T) {
	t.Run("keeps the client default without overrides", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "")
		t.Setenv("SLACK_MCP_GOVSLACK", "")
		assert.Equal(t, "", initialAPIURL())
	})

	t.Run("uses the GovSlack endpoint in GovSlack mode", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "")
		t.Setenv("SLACK_MCP_GOVSLACK", "true")
		assert.Equal(t, "https://slack-gov.com/api/", initialAPIURL())
	})

	t.Run("prefers SLACK_MCP_API_URL and adds the trailing slash", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "http://localhost:4003/api")
		t.Setenv("SLACK_MCP_GOVSLACK", "true")
		assert.Equal(t, "http://localhost:4003/api/", initialAPIURL())
	})
}

func TestUnitWorkspaceAPIURL(t *testing.T) {
	t.Run("derives the API base from the workspace URL", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "")
		assert.Equal(t, "https://acme.slack.com/api/", workspaceAPIURL("https://acme.slack.com/"))
	})

	t.Run("keeps SLACK_MCP_API_URL after authentication", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "http://localhost:4003/api/")
		assert.Equal(t, "http://localhost:4003/api/", workspaceAPIURL("https://acme.slack.com/"))
	})
}

func TestUnitAPIURLWarnings(t *testing.T) {
	t.Run("says nothing without an override", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "")
		t.Setenv("SLACK_MCP_GOVSLACK", "true")
		assert.Empty(t, apiURLWarnings())
	})

	t.Run("allows plain http on the local machine", func(t *testing.T) {
		t.Setenv("SLACK_MCP_GOVSLACK", "")
		for _, apiURL := range []string{"http://localhost:4003/api/", "http://127.0.0.1:4003/api/", "http://[::1]:4003/api/", "http://slack.localhost/api/"} {
			t.Setenv("SLACK_MCP_API_URL", apiURL)
			assert.Empty(t, apiURLWarnings(), apiURL)
		}
	})

	t.Run("warns about plain http to another host", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "http://slack-sim:4003/api/")
		t.Setenv("SLACK_MCP_GOVSLACK", "")
		warnings := apiURLWarnings()
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "unencrypted")
	})

	t.Run("does not warn about https", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "https://slack.example.com/api/")
		t.Setenv("SLACK_MCP_GOVSLACK", "")
		assert.Empty(t, apiURLWarnings())
	})

	t.Run("warns that GovSlack mode is ignored", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "https://slack.example.com/api/")
		t.Setenv("SLACK_MCP_GOVSLACK", "true")
		warnings := apiURLWarnings()
		assert.Len(t, warnings, 1)
		assert.Contains(t, warnings[0], "SLACK_MCP_GOVSLACK")
	})
}
