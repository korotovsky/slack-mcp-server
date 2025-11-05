# OAuth Multi-User Setup Guide

This guide covers setting up OAuth 2.0 authentication for multi-user support in the Slack MCP Server.

## Overview

OAuth mode allows multiple users to authenticate with their own Slack accounts, providing:
- ✅ Per-user token isolation
- ✅ Standard OAuth 2.0 flow
- ✅ Secure token management
- ✅ No need to extract browser tokens

## Prerequisites

- Slack workspace admin access
- Go 1.21+ (for development)
- ngrok (required for local development - Slack requires HTTPS)
- 25 minutes for initial setup

---

## Step 1: Create Slack App (15 min)

### 1.1 Create the App

1. Visit [api.slack.com/apps](https://api.slack.com/apps)
2. Click "Create New App" → "From scratch"
3. Name: "Slack MCP OAuth" (or your preferred name)
4. Select your workspace

### 1.2 Add OAuth Scopes

Navigate to **OAuth & Permissions** → **User Token Scopes** and add:

```
channels:history, channels:read
groups:history, groups:read
im:history, im:read, im:write
mpim:history, mpim:read, mpim:write
users:read, chat:write, search:read
```

### 1.3 Setup ngrok (REQUIRED)

**Important**: Slack requires HTTPS for all redirect URIs, including localhost.

```bash
# Install ngrok
brew install ngrok
# or download from https://ngrok.com/download

# Start ngrok tunnel (keep this running in a separate terminal)
ngrok http 13080
```

You'll see output like:
```
Forwarding  https://abc123-456-789.ngrok-free.app -> http://localhost:13080
```

**Copy the HTTPS URL** (e.g., `https://abc123-456-789.ngrok-free.app`)

### 1.4 Configure Redirect URL

In your Slack app settings:

1. Go to **OAuth & Permissions** → **Redirect URLs**
2. Add your ngrok URL with the callback path:
   ```
   https://your-ngrok-id.ngrok-free.app/oauth/callback
   ```
3. Click "Save URLs"

### 1.5 Get Credentials

Navigate to **Basic Information** → **App Credentials**:
- Copy **Client ID**
- Copy **Client Secret**

---

## Step 2: Configure Server (2 min)

### 2.1 Create Configuration File

```bash
# Copy the example template
cp oauth.env.example oauth.env

# Edit with your credentials
nano oauth.env
```

### 2.2 Set Your Credentials

Update `oauth.env` with your values:

```bash
SLACK_MCP_OAUTH_ENABLED=true
SLACK_MCP_OAUTH_CLIENT_ID=your_client_id_here
SLACK_MCP_OAUTH_CLIENT_SECRET=your_client_secret_here

# Use your ngrok HTTPS URL:
SLACK_MCP_OAUTH_REDIRECT_URI=https://your-ngrok-id.ngrok-free.app/oauth/callback

# Server configuration
SLACK_MCP_HOST=127.0.0.1
SLACK_MCP_PORT=13080
```

### 2.3 Load Configuration

```bash
source oauth.env
```

---

## Step 3: Start Server (1 min)

**Ensure ngrok is still running from Step 1.3!**

```bash
go run cmd/slack-mcp-server/main.go -t http
```

Expected output:
```
OAuth mode enabled
OAuth endpoints enabled
HTTP server listening on http://127.0.0.1:13080
```

**Note**: The server runs on localhost, but OAuth callbacks come through ngrok HTTPS.

---

## Step 4: Authenticate Users (5 min per user)

### 4.1 Run Authentication Script

```bash
# Set your ngrok HTTPS URL
export OAUTH_SERVER_URL=https://your-ngrok-id.ngrok-free.app

# Run the test script
./scripts/test-oauth.sh
```

### 4.2 Complete OAuth Flow

The script will:
1. Display an OAuth authorization URL
2. Open your browser automatically
3. Ask you to authorize in Slack
4. Redirect you to the callback URL
5. Prompt you to paste the callback URL
6. Extract and display your access token

**Save your access token!** You'll need it to configure your MCP client.

---

## Step 5: Configure MCP Client

### For Claude Desktop / Cursor

Use your ngrok HTTPS URL and the access token from Step 4:

```json
{
  "mcpServers": {
    "slack": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "https://your-ngrok-id.ngrok-free.app/mcp",
        "--header",
        "Authorization: Bearer YOUR_ACCESS_TOKEN"
      ]
    }
  }
}
```

### Test with curl

```bash
curl -H "Authorization: Bearer YOUR_TOKEN" \
     -X POST https://your-ngrok-id.ngrok-free.app/mcp \
     -H "Content-Type: application/json" \
     -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

---

## Multiple Users

To set up multiple users:

```bash
# Set ngrok URL for all users
export OAUTH_SERVER_URL=https://your-ngrok-id.ngrok-free.app

# User A authenticates
./scripts/test-oauth.sh  # Save token A

# User B authenticates
./scripts/test-oauth.sh  # Save token B

# Each user uses their own token in their MCP client config
```

---

## Architecture

### How It Works

```
User Request + OAuth Token
    ↓
Middleware: Validate token → Extract userID
    ↓
Handler: Create Slack client with user's token
    ↓
API call as that user
    ↓
Client discarded after request
```

### Per-Request Client Approach

Each API request:
1. Validates the OAuth token
2. Creates a new `slack.Client` with the user's access token
3. Makes the API call
4. Discards the client

**Benefits**: Simple, stateless, perfect token isolation  
**Trade-off**: +2-3ms per request (negligible for most use cases)

---

## OAuth Endpoints

When OAuth mode is enabled, the server exposes:

- `GET /oauth/authorize` - Initiates OAuth flow, returns authorization URL
- `GET /oauth/callback?code=xxx&state=yyy` - Handles callback, exchanges code for token
- `POST /mcp` - MCP endpoint (requires `Authorization: Bearer token` header)

---

## Environment Variables Reference

### OAuth Mode (All Required)
```bash
SLACK_MCP_OAUTH_ENABLED=true
SLACK_MCP_OAUTH_CLIENT_ID=xxx
SLACK_MCP_OAUTH_CLIENT_SECRET=xxx
SLACK_MCP_OAUTH_REDIRECT_URI=https://your-ngrok-id.ngrok-free.app/oauth/callback
```

### Server Configuration
```bash
SLACK_MCP_HOST=127.0.0.1
SLACK_MCP_PORT=13080
```

### Legacy Mode (Alternative - Still Supported)
```bash
SLACK_MCP_OAUTH_ENABLED=false
SLACK_MCP_XOXP_TOKEN=xoxp-...
```

---

## Troubleshooting

### "Missing credentials"
```bash
# Make sure environment is loaded
source oauth.env

# Verify variables are set
echo $SLACK_MCP_OAUTH_CLIENT_ID
```

### "Invalid redirect_uri"
- Slack app redirect URL must exactly match your `oauth.env` setting
- Must use HTTPS (Slack requirement - no exceptions)
- Check both Slack app settings and `oauth.env`
- Example: `https://abc123.ngrok-free.app/oauth/callback`

### "Invalid or expired state"
- OAuth authorization codes expire in 10 minutes
- Start the flow again from the beginning

### "Token not found"
- Server was restarted (tokens are stored in-memory)
- Re-authenticate: `./scripts/test-oauth.sh`

### ngrok URL Changed
- Free ngrok URLs change on restart
- Update both:
  1. Slack app redirect URL settings
  2. `oauth.env` file
- Restart server and re-authenticate users

### Server Compilation Errors
```bash
# Clean and rebuild
go clean -cache
go build ./cmd/slack-mcp-server
```

---

## Limitations (Demo Mode)

Current implementation has these limitations:

⚠️ **In-memory storage**: Tokens are lost when server restarts  
⚠️ **No caching**: New client created per request  
⚠️ **HTTP/SSE only**: OAuth mode not compatible with stdio transport  
⚠️ **ngrok dependency**: Free tier URLs change on restart

These limitations are acceptable for:
- Development and testing
- Small teams (2-10 users)
- Proof of concept deployments

For production use, consider:
- Persistent token storage (database)
- Client connection pooling
- Production-grade HTTPS (not ngrok)

---

## Security Considerations

- ✅ CSRF protection via state parameter
- ✅ Per-user token isolation
- ✅ Tokens stored in-memory only
- ⚠️ Use HTTPS in production (required by Slack)
- ⚠️ Keep client secrets secure
- ⚠️ Don't commit `oauth.env` to version control

---

## Next Steps

1. **Development**: Test with multiple users using `./scripts/test-oauth.sh`
2. **Production**: Set up persistent storage and proper HTTPS
3. **Integration**: Configure MCP clients (Claude, Cursor) with user tokens

For basic authentication setup without OAuth, see [Authentication Setup](01-authentication-setup.md).

