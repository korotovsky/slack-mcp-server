#!/bin/bash

set -e

cd "$(dirname "$0")"

echo "=== Starting OAuth-enabled Slack MCP Server ==="
echo ""

# Load environment
if [ ! -f oauth.env.example ]; then
    echo "❌ oauth.env.example not found!"
    exit 1
fi

echo "Loading environment from oauth.env.example..."
source oauth.env.example

# Verify critical env vars
if [ -z "$SLACK_MCP_OAUTH_CLIENT_ID" ]; then
    echo "❌ SLACK_MCP_OAUTH_CLIENT_ID not set in oauth.env.example"
    exit 1
fi

if [ -z "$SLACK_MCP_OAUTH_CLIENT_SECRET" ]; then
    echo "❌ SLACK_MCP_OAUTH_CLIENT_SECRET not set in oauth.env.example"
    exit 1
fi

if [ -z "$SLACK_MCP_OAUTH_REDIRECT_URI" ]; then
    echo "❌ SLACK_MCP_OAUTH_REDIRECT_URI not set in oauth.env.example"
    exit 1
fi

echo "✅ Environment loaded:"
echo "   Client ID: ${SLACK_MCP_OAUTH_CLIENT_ID:0:20}..."
echo "   Redirect URI: $SLACK_MCP_OAUTH_REDIRECT_URI"
echo ""

# Check if ngrok URL is being used
if [[ "$SLACK_MCP_OAUTH_REDIRECT_URI" == https://*ngrok* ]]; then
    echo "✅ Using ngrok HTTPS URL (required by Slack)"
else
    echo "⚠️  WARNING: Not using ngrok HTTPS URL"
    echo "   Slack requires HTTPS for OAuth redirect URIs"
fi

echo ""
echo "Starting server (this will show download progress first time)..."
echo "Once started, you'll see: 'OAuth mode enabled' and 'HTTP server listening'"
echo ""
echo "Press Ctrl+C to stop the server"
echo ""
echo "----------------------------------------"
echo ""

# Start server (use custom cache to avoid permission errors)
GOMODCACHE=/tmp/gomodcache go run cmd/slack-mcp-server/main.go -t http



