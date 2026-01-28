#!/bin/bash

set -e

BASE_URL="${OAUTH_SERVER_URL:-http://localhost:13080}"

echo "=== Slack MCP OAuth Setup ==="
echo ""
echo "⚠️  IMPORTANT: Slack requires HTTPS for OAuth"
echo ""

if [[ "$BASE_URL" == http://localhost:* ]]; then
    echo "WARNING: You're using http://localhost"
    echo "Slack OAuth requires HTTPS even for local development!"
    echo ""
    echo "Please:"
    echo "1. Install ngrok: brew install ngrok"
    echo "2. Run: ngrok http 13080"
    echo "3. Set: export OAUTH_SERVER_URL=https://your-ngrok-id.ngrok-free.app"
    echo "4. Update oauth.env with your ngrok HTTPS URL"
    echo "5. Add the ngrok URL to your Slack app's redirect URLs"
    echo ""
    read -p "Continue anyway? (y/N) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        exit 1
    fi
fi

echo "Using OAuth server: $BASE_URL"
echo ""

# Step 1: Get authorization URL
echo "1. Getting authorization URL..."
AUTH_RESPONSE=$(curl -s "$BASE_URL/oauth/authorize")

if [ $? -ne 0 ]; then
    echo "Error: Could not connect to $BASE_URL"
    echo "Make sure the server is running with OAuth enabled"
    exit 1
fi

AUTH_URL=$(echo "$AUTH_RESPONSE" | jq -r '.authorization_url')
STATE=$(echo "$AUTH_RESPONSE" | jq -r '.state')

if [ "$AUTH_URL" == "null" ] || [ -z "$AUTH_URL" ]; then
    echo "Error: Failed to get authorization URL"
    echo "Response: $AUTH_RESPONSE"
    exit 1
fi

echo ""
echo "2. Visit this URL to authorize:"
echo ""
echo "   $AUTH_URL"
echo ""

# Try to open browser automatically
if command -v open &> /dev/null; then
    open "$AUTH_URL" 2>/dev/null || true
elif command -v xdg-open &> /dev/null; then
    xdg-open "$AUTH_URL" 2>/dev/null || true
fi

echo "3. After authorizing, Slack will redirect to a URL like:"
echo "   http://localhost:13080/oauth/callback?code=...&state=..."
echo ""
echo "Paste the entire callback URL here:"
read -p "Callback URL: " CALLBACK_URL

# Extract code from URL
CODE=$(echo "$CALLBACK_URL" | sed -n 's/.*code=\([^&]*\).*/\1/p')

if [ -z "$CODE" ]; then
    echo "Error: Could not extract code from URL"
    echo "Make sure you copied the full callback URL"
    exit 1
fi

echo ""
echo "4. Exchanging code for access token..."

TOKEN_RESPONSE=$(curl -s "$BASE_URL/oauth/callback?code=$CODE&state=$STATE")

if [ $? -ne 0 ]; then
    echo "Error: Token exchange failed"
    exit 1
fi

ACCESS_TOKEN=$(echo "$TOKEN_RESPONSE" | jq -r '.access_token')
USER_ID=$(echo "$TOKEN_RESPONSE" | jq -r '.user_id')
TEAM_ID=$(echo "$TOKEN_RESPONSE" | jq -r '.team_id')
MESSAGE=$(echo "$TOKEN_RESPONSE" | jq -r '.message')

if [ "$ACCESS_TOKEN" == "null" ] || [ -z "$ACCESS_TOKEN" ]; then
    echo "Error: Failed to get access token"
    echo "Response: $TOKEN_RESPONSE"
    exit 1
fi

echo ""
echo "=== Success! ==="
echo ""
echo "User ID: $USER_ID"
echo "Team ID: $TEAM_ID"
echo ""
echo "Access Token:"
echo "$ACCESS_TOKEN"
echo ""
echo "---------------------------------------"
echo "Use this token in your MCP client:"
echo "---------------------------------------"
echo ""
echo "For Claude Desktop/Cursor config:"
echo ""
echo '{
  "mcpServers": {
    "slack": {
      "command": "npx",
      "args": ["-y", "mcp-remote", "'$BASE_URL'"],
      "env": {
        "SLACK_OAUTH_TOKEN": "'$ACCESS_TOKEN'"
      }
    }
  }
}'
echo ""
echo "Or use as HTTP header:"
echo "Authorization: Bearer $ACCESS_TOKEN"
echo ""

