## 3. Configuration and Usage

You can configure the MCP server using command line arguments and environment variables.

### Using DXT

For [Claude Desktop](https://claude.ai/download) users, you can use the DXT extension to run the MCP server without needing to edit the `claude_desktop_config.json` file directly. Download the [latest version](https://github.com/korotovsky/slack-mcp-server/releases/latest/download/slack-mcp-server.dxt) of the DXT Extension from [releases](https://github.com/korotovsky/slack-mcp-server/releases) page.

1. Open Claude Desktop and go to the `Settings` menu.
2. Click on the `Extensions` tab.
3. Drag and drop the downloaded .dxt file to install it and click "Install".
5. Fill all required configuration fields
    - Authentication method: `xoxc/xoxd` or `xoxp`.
    - Value for `SLACK_MCP_XOXC_TOKEN` and `SLACK_MCP_XOXD_TOKEN` in case of `xoxc/xoxd` method, or `SLACK_MCP_XOXP_TOKEN` in case of `xoxp`.
    - You may also enable `Add Message Tool` to allow posting messages to channels.
    - You may also change User-Agent if needed if you have Enterprise Slack.
6. Enable MCP Server.

> [!IMPORTANT]
> You may need to disable bundled node in Claude Desktop and let it use node from host machine to avoid some startup issues in case you encounter them. It is DXT known bug: https://github.com/anthropics/dxt/issues/45#issuecomment-3050284228

### Using Cursor Installer

The MCP server can be installed using the Cursor One-Click method.

Below are prepared configurations:

 - `npx` and `xoxc/xoxd` method: [![Install MCP Server](https://cursor.com/deeplink/mcp-install-light.svg)](cursor://anysphere.cursor-deeplink/mcp/install?name=slack-mcp-server&config=eyJjb21tYW5kIjogIm5weCAteSBzbGFjay1tY3Atc2VydmVyQGxhdGVzdCAtLXRyYW5zcG9ydCBzdGRpbyIsImVudiI6IHsiU0xBQ0tfTUNQX1hPWENfVE9LRU4iOiAieG94Yy0uLi4iLCAiU0xBQ0tfTUNQX1hPWERfVE9LRU4iOiAieG94ZC0uLi4ifSwiZGlzYWJsZWQiOiBmYWxzZSwiYXV0b0FwcHJvdmUiOiBbXX0%3D)
 - `npx` and `xoxp` method: [![Install MCP Server](https://cursor.com/deeplink/mcp-install-light.svg)](cursor://anysphere.cursor-deeplink/mcp/install?name=slack-mcp-server&config=eyJjb21tYW5kIjogIm5weCAteSBzbGFjay1tY3Atc2VydmVyQGxhdGVzdCAtLXRyYW5zcG9ydCBzdGRpbyIsImVudiI6IHsiU0xBQ0tfTUNQX1hPWFBfVE9LRU4iOiAieG94cC0uLi4ifSwiZGlzYWJsZWQiOiBmYWxzZSwiYXV0b0FwcHJvdmUiOiBbXX0%3D)

> [!IMPORTANT]
> Remember to replace tokens in the configuration with your own tokens, as they are just examples.

### Using npx

If you have npm installed, this is the fastest way to get started with `slack-mcp-server` on Claude Desktop.

Open your `claude_desktop_config.json` and add the mcp server to the list of `mcpServers`:

> [!WARNING]  
> If you are using Enterprise Slack, you may set `SLACK_MCP_USER_AGENT` environment variable to match your browser's User-Agent string from where you extracted `xoxc` and `xoxd` and enable `SLACK_MCP_CUSTOM_TLS` to enable custom TLS-handshakes to start to look like a real browser. This is required for the server to work properly in some environments with higher security policies.

**Option 1: Using XOXP Token**
``` json
{
  "mcpServers": {
    "slack": {
      "command": "npx",
      "args": [
        "-y",
        "slack-mcp-server@latest",
        "--transport",
        "stdio"
      ],
      "env": {
        "SLACK_MCP_XOXP_TOKEN": "xoxp-..."
      }
    }
  }
}
```

**Option 2: Using XOXC/XOXD Tokens**
``` json
{
  "mcpServers": {
    "slack": {
      "command": "npx",
      "args": [
        "-y",
        "slack-mcp-server@latest",
        "--transport",
        "stdio"
      ],
      "env": {
        "SLACK_MCP_XOXC_TOKEN": "xoxc-...",
        "SLACK_MCP_XOXD_TOKEN": "xoxd-..."
      }
    }
  }
}
```

<details>
<summary>Or, stdio transport with docker.</summary>

**Option 1: Using XOXP Token**
```json
{
  "mcpServers": {
    "slack": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "-e",
        "SLACK_MCP_XOXP_TOKEN",
        "ghcr.io/korotovsky/slack-mcp-server",
        "mcp-server",
        "--transport",
        "stdio"
      ],
      "env": {
        "SLACK_MCP_XOXP_TOKEN": "xoxp-..."
      }
    }
  }
}
```

**Option 2: Using XOXC/XOXD Tokens**
```json
{
  "mcpServers": {
    "slack": {
      "command": "docker",
      "args": [
        "run",
        "-i",
        "--rm",
        "-e",
        "SLACK_MCP_XOXC_TOKEN",
        "-e",
        "SLACK_MCP_XOXD_TOKEN",
        "ghcr.io/korotovsky/slack-mcp-server",
        "mcp-server",
        "--transport",
        "stdio"
      ],
      "env": {
        "SLACK_MCP_XOXC_TOKEN": "xoxc-...",
        "SLACK_MCP_XOXD_TOKEN": "xoxd-..."
      }
    }
  }
}
```

Please see [Docker](#Using-Docker) for more information.
</details>

### Using npx with `sse` transport:

In case you would like to run it in `sse` mode, then you  should use `mcp-remote` wrapper for Claude Desktop and deploy/expose MCP server somewhere e.g. with `ngrok` or `docker-compose`.

```json
{
  "mcpServers": {
    "slack": {
      "command": "npx",
      "args": [
        "-y",
        "mcp-remote",
        "https://x.y.z.q:3001/sse",
        "--header",
        "Authorization: Bearer ${SLACK_MCP_API_KEY}"
      ],
      "env": {
        "SLACK_MCP_API_KEY": "my-$$e-$ecret"
      }
    }
  }
}
```

<details>
<summary>Or, sse transport for Windows.</summary>

```json
{
  "mcpServers": {
    "slack": {
      "command": "C:\\Progra~1\\nodejs\\npx.cmd",
      "args": [
        "-y",
        "mcp-remote",
        "https://x.y.z.q:3001/sse",
        "--header",
        "Authorization: Bearer ${SLACK_MCP_API_KEY}"
      ],
      "env": {
        "SLACK_MCP_API_KEY": "my-$$e-$ecret"
      }
    }
  }
}
```
</details>

### TLS and Exposing to the Internet

There are several reasons why you might need to setup HTTPS for your SSE.
- `mcp-remote` is capable to handle only https schemes;
- it is generally a good practice to use TLS for any service exposed to the internet;

You could use `ngrok`:

```bash
ngrok http 3001
```

and then use the endpoint `https://903d-xxx-xxxx-xxxx-10b4.ngrok-free.app` for your `mcp-remote` argument.

### Using Docker

For detailed information about all environment variables, see [Environment Variables](https://github.com/korotovsky/slack-mcp-server?tab=readme-ov-file#environment-variables).

```bash
export SLACK_MCP_XOXC_TOKEN=xoxc-...
export SLACK_MCP_XOXD_TOKEN=xoxd-...

docker pull ghcr.io/korotovsky/slack-mcp-server:latest
docker run -i --rm \
  -e SLACK_MCP_XOXC_TOKEN \
  -e SLACK_MCP_XOXD_TOKEN \
  slack-mcp-server mcp-server --transport stdio
```

Or, the docker-compose way:

```bash
wget -O docker-compose.yml https://github.com/korotovsky/slack-mcp-server/releases/latest/download/docker-compose.yml
wget -O .env https://github.com/korotovsky/slack-mcp-server/releases/latest/download/default.env.dist
nano .env # Edit .env file with your tokens from step 1 of the setup guide
docker network create app-tier
docker-compose up -d
```

### Console Arguments

| Argument              | Required ? | Description                                                              |
|-----------------------|------------|--------------------------------------------------------------------------|
| `--transport` or `-t` | Yes        | Select transport for the MCP Server, possible values are: `stdio`, `sse` |

### Environment Variables

| Variable                          | Required? | Default                   | Description                                                                                                                                                                                                                                                                               |
|-----------------------------------|-----------|---------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `SLACK_MCP_XOXC_TOKEN`            | Yes*      | `nil`                     | Slack browser token (`xoxc-...`)                                                                                                                                                                                                                                                          |
| `SLACK_MCP_XOXD_TOKEN`            | Yes*      | `nil`                     | Slack browser cookie `d` (`xoxd-...`)                                                                                                                                                                                                                                                     |
| `SLACK_MCP_XOXP_TOKEN`            | Yes*      | `nil`                     | User OAuth token (`xoxp-...`) — alternative to xoxc/xoxd                                                                                                                                                                                                                                  |
| `SLACK_MCP_PORT`                  | No        | `13080`                   | Port for the MCP server to listen on                                                                                                                                                                                                                                                      |
| `SLACK_MCP_HOST`                  | No        | `127.0.0.1`               | Host for the MCP server to listen on                                                                                                                                                                                                                                                      |
| `SLACK_MCP_API_KEY`               | No        | `nil`                     | Bearer token for SSE and HTTP transports                                                                                                                                                                                                                                                  |
| `SLACK_OAUTH_CLIENT_ID`           | No        | `nil`                     | Slack OAuth2 app client ID (for OAuth2 flow)                                                                                                                                                                                                                                              |
| `SLACK_OAUTH_CLIENT_SECRET`       | No        | `nil`                     | Slack OAuth2 app client secret (for OAuth2 flow)                                                                                                                                                                                                                                          |
| `SLACK_OAUTH_REDIRECT_URI`        | No        | `nil`                     | OAuth2 redirect URI (e.g., `https://your-server:3001/oauth/callback`)                                                                                                                                                                                                                     |
| `MCP_OAUTH_CLIENT_ID`             | No        | `nil`                     | MCP OAuth2 client ID (for authenticating LiteLLM clients)                                                                                                                                                                                                                                 |
| `MCP_OAUTH_CLIENT_SECRET`         | No        | `nil`                     | MCP OAuth2 client secret (for authenticating LiteLLM clients)                                                                                                                                                                                                                             |
| `SLACK_MCP_PROXY`                 | No        | `nil`                     | Proxy URL for outgoing requests                                                                                                                                                                                                                                                           |
| `SLACK_MCP_USER_AGENT`            | No        | `nil`                     | Custom User-Agent (for Enterprise Slack environments)                                                                                                                                                                                                                                     |
| `SLACK_MCP_CUSTOM_TLS`            | No        | `nil`                     | Send custom TLS-handshake to Slack servers based on `SLACK_MCP_USER_AGENT` or default User-Agent. (for Enterprise Slack environments)                                                                                                                                                     |
| `SLACK_MCP_SERVER_CA`             | No        | `nil`                     | Path to CA certificate                                                                                                                                                                                                                                                                    |
| `SLACK_MCP_SERVER_CA_TOOLKIT`     | No        | `nil`                     | Inject HTTPToolkit CA certificate to root trust-store for MitM debugging                                                                                                                                                                                                                  |
| `SLACK_MCP_SERVER_CA_INSECURE`    | No        | `false`                   | Trust all insecure requests (NOT RECOMMENDED)                                                                                                                                                                                                                                             |
| `SLACK_MCP_ADD_MESSAGE_TOOL`      | No        | `nil`                     | Enable message posting via `conversations_add_message` by setting it to true for all channels, a comma-separated list of channel IDs to whitelist specific channels, or use `!` before a channel ID to allow all except specified ones, while an empty value disables posting by default. |
| `SLACK_MCP_ADD_MESSAGE_MARK`      | No        | `nil`                     | When the `conversations_add_message` tool is enabled, any new message sent will automatically be marked as read.                                                                                                                                                                          |
| `SLACK_MCP_ADD_MESSAGE_UNFURLING` | No        | `nil`                     | Enable to let Slack unfurl posted links or set comma-separated list of domains e.g. `github.com,slack.com` to whitelist unfurling only for them. If text contains whitelisted and unknown domain unfurling will be disabled for security reasons.                                         |
| `SLACK_MCP_USERS_CACHE`           | No        | `.users_cache.json`       | Path to the users cache file. Used to cache Slack user information to avoid repeated API calls on startup.                                                                                                                                                                                |
| `SLACK_MCP_CHANNELS_CACHE`        | No        | `.channels_cache_v2.json` | Path to the channels cache file. Used to cache Slack channel information to avoid repeated API calls on startup.                                                                                                                                                                          |
| `SLACK_MCP_LOG_LEVEL`             | No        | `info`                    | Log-level for stdout or stderr. Valid values are: `debug`, `info`, `warn`, `error`, `panic` and `fatal`                                                                                                                                                                                   |

### OAuth2 Flow (Recommended for LiteLLM)

The MCP server supports OAuth2 authentication, which is ideal for integrating with LiteLLM and other OAuth2-aware clients. This provides a secure, standard way to authenticate users and manage multiple Slack workspaces.

#### Setup

1. **Create a Slack OAuth App** at [api.slack.com/apps](https://api.slack.com/apps)
   - Add OAuth scopes (see [Authentication Setup](01-authentication-setup.md) for required scopes)
   - Set redirect URI to `https://your-server:port/oauth/callback`
   - Note your Client ID and Client Secret

2. **Configure Environment Variables:**
```bash
# Slack OAuth app credentials
export SLACK_OAUTH_CLIENT_ID="your-slack-client-id"
export SLACK_OAUTH_CLIENT_SECRET="your-slack-client-secret"
export SLACK_OAUTH_REDIRECT_URI="https://your-server:3001/oauth/callback"

# Optional: MCP OAuth credentials (for client authentication)
export MCP_OAUTH_CLIENT_ID="your-mcp-client-id"
export MCP_OAUTH_CLIENT_SECRET="your-mcp-client-secret"
```

3. **Start the MCP server with SSE or HTTP transport:**
```bash
npx slack-mcp-server@latest --transport sse
```

4. **OAuth2 Endpoints Available:**
- `GET /oauth/authorize` - Initiate OAuth flow
- `GET /oauth/callback` - Handle Slack OAuth callback
- `POST /oauth/token` - Token exchange endpoint

#### Using with LiteLLM

Configure LiteLLM to use OAuth2:

```yaml
mcp_servers:
  slack:
    url: "https://your-server:3001"
    transport: "http"
    auth_type: "oauth2"
    authorization_url: "https://your-server:3001/oauth/authorize"
    token_url: "https://your-server:3001/oauth/token"
    client_id: "your-mcp-client-id"  # Optional if MCP_OAUTH_CLIENT_ID not set
    client_secret: "your-mcp-secret"  # Optional if MCP_OAUTH_CLIENT_SECRET not set
    scopes: ["slack:read", "slack:write"]
```

#### How It Works

1. LiteLLM redirects user to `/oauth/authorize`
2. MCP server redirects to Slack OAuth
3. User authorizes the Slack app
4. Slack redirects back to `/oauth/callback`
5. MCP server exchanges code for Slack token
6. MCP server generates an MCP access token and stores the mapping
7. User receives the MCP access token
8. LiteLLM uses this token in subsequent requests
9. MCP server looks up the corresponding Slack token and makes Slack API calls

#### Token Management

- Tokens are cached in memory (persists until server restart)
- Default expiration: 90 days
- Each unique user/workspace gets a separate token
- Tokens can be revoked by restarting the server

### Per-Request OAuth Tokens

The MCP server also supports per-request OAuth tokens for direct Slack token authentication, allowing you to authenticate each request with a different Slack workspace or user account. This is particularly useful for:
- Multi-tenant applications
- Serving multiple Slack workspaces from a single server instance
- Dynamic token management

#### How It Works

When using SSE or HTTP transports, you can provide a Slack OAuth token in the `Authorization` header of each request. The server will:
1. Detect if the token is a Slack OAuth token (by checking for `xoxp-`, `xoxc-`, `xoxb-`, or `xoxd-` prefixes)
2. Create a new Slack API client for that token
3. Use that client for the request
4. Cache the client for future requests with the same token

#### Usage

**Per-Request Token (overrides environment token):**
```bash
curl -X POST https://your-server:13080/mcp \
  -H "Authorization: Bearer xoxp-your-oauth-token" \
  -H "Content-Type: application/json" \
  -d '{"method": "tools/list"}'
```

**Fallback to Environment Token:**
If no per-request token is provided, the server falls back to the token configured via environment variables (`SLACK_MCP_XOXP_TOKEN`, `SLACK_MCP_XOXC_TOKEN`, etc.).

#### Important Notes

- **Token Priority:** Per-request tokens take priority over environment tokens when provided
- **Token Caching:** The server caches clients per token for performance. Each unique token creates a new client instance
- **Session Tokens:** When using `xoxc-` session tokens per-request, note that the corresponding `xoxd-` token cannot be passed in the same header. For session-based auth, it's recommended to use environment variables
- **Stdio Transport:** Per-request tokens are only available for SSE and HTTP transports. The stdio transport always uses environment variables
- **API Key Authentication:** The server can differentiate between API keys (for server authentication) and Slack OAuth tokens (for Slack API access) based on token prefixes
