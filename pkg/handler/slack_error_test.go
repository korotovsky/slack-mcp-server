package handler

import (
	"encoding/json"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
)

// TestSlackErrorStringUnmarshal reproduces the bug where Slack API returns
// an "errors" field as an array of strings instead of objects.
// Error: json: cannot unmarshal string into Go struct field
// chatResponseFull.SlackResponse.errors of type map[string]interface{}
func TestSlackErrorStringUnmarshal(t *testing.T) {
	// This is what Slack sometimes returns - errors as array of strings
	jsonWithStringErrors := `{
		"ok": false,
		"error": "invalid_blocks",
		"errors": ["block_validation_error"]
	}`

	// This response embeds SlackResponse just like chatResponseFull does
	type testResponse struct {
		Channel string `json:"channel"`
		slack.SlackResponse
	}

	var resp testResponse
	err := json.Unmarshal([]byte(jsonWithStringErrors), &resp)

	// This currently FAILS with:
	// json: cannot unmarshal string into Go struct field
	// testResponse.SlackResponse.errors of type map[string]interface{}
	assert.NoError(t, err, "Should handle string errors from Slack API")
	assert.False(t, resp.Ok)
	assert.Equal(t, "invalid_blocks", resp.Error)
}

// TestSlackErrorObjectUnmarshal verifies that object errors work correctly
func TestSlackErrorObjectUnmarshal(t *testing.T) {
	// This is the normal case where errors are objects
	jsonWithObjectErrors := `{
		"ok": false,
		"error": "invalid_blocks",
		"errors": [{"pointer": "/blocks/0", "message": "Invalid block"}]
	}`

	type testResponse struct {
		Channel string `json:"channel"`
		slack.SlackResponse
	}

	var resp testResponse
	err := json.Unmarshal([]byte(jsonWithObjectErrors), &resp)

	assert.NoError(t, err, "Should handle object errors from Slack API")
	assert.False(t, resp.Ok)
	assert.Equal(t, "invalid_blocks", resp.Error)
}
