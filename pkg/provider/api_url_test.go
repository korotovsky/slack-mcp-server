package provider

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInitialAPIURL(t *testing.T) {
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

func TestWorkspaceAPIURL(t *testing.T) {
	t.Run("derives the API base from the workspace URL", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "")
		assert.Equal(t, "https://acme.slack.com/api/", workspaceAPIURL("https://acme.slack.com/"))
	})

	t.Run("keeps SLACK_MCP_API_URL after authentication", func(t *testing.T) {
		t.Setenv("SLACK_MCP_API_URL", "http://localhost:4003/api/")
		assert.Equal(t, "http://localhost:4003/api/", workspaceAPIURL("https://acme.slack.com/"))
	})
}
