# Slack MCP Server

Model Context Protocol (MCP) server for Slack Workspaces. This integration supports both Stdio and SSE transports, proxy settings and does not require any permissions or bots being created or approved by Workspace admins 😏.

### Feature Demo

![ezgif-316311ee04f444](https://github.com/user-attachments/assets/35dc9895-e695-4e56-acdc-1a46d6520ba0)

## Tools

1. `conversations_history`
  - Get messages from the channel by channelID
  - Required inputs:
    - `channel_id` (string): ID of the channel in format Cxxxxxxxxxx.
    - `cursor` (string): Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request.
    - `limit` (number, default: 28): Limit of messages to fetch.
  - Returns: List of messages with timestamps, user IDs, and text content

2. `channels_list`
  - Get list of channels
  - Required inputs:
    - `channel_types` (array): Possible channel types. Allowed values: 'mpim', 'im', 'public_channel', 'private_channel'.
    - `sort` (string): Type of sorting. Allowed values: 'popularity' - sort by number of members/participants in each channel.
  - Returns: List of channels

## Setup Guide

### 1. Authentication Setup

Open up your Slack in your browser and login.

#### Lookup `SLACK_MCP_XOXC_TOKEN`

- Open your browser's Developer Console.
- In Firefox, under `Tools -> Browser Tools -> Web Developer tools` in the menu bar
- In Chrome, click the "three dots" button to the right of the URL Bar, then select
`More Tools -> Developer Tools`
- Switch to the console tab.
- Type "allow pasting" and press ENTER.
- Paste the following snippet and press ENTER to execute:
  `JSON.parse(localStorage.localConfig_v2).teams[document.location.pathname.match(/^\/client\/([A-Z0-9]+)/)[1]].token`

Token value is printed right after the executed command (it starts with
`xoxc-`), save it somewhere for now.

#### Lookup `SLACK_MCP_XOXD_TOKEN`

 - Switch to "Application" tab and select "Cookies" in the left navigation pane.
 - Find the cookie with the name `d`.  That's right, just the letter `d`.
 - Double-click the Value of this cookie.
 - Press Ctrl+C or Cmd+C to copy it's value to clipboard.
 - Save it for later.

### 2. Installation

Choose one of these installation methods:

- [npx](#Using-npx)
- [Docker](#Using-Docker)

### 3. Configuration and Usage

You can configure the MCP server using command line arguments and environment variables.

#### Using npx

If you have npm installed, this is the fastest way to get started with `kubernetes-mcp-server` on Claude Desktop.

Open your `claude_desktop_config.json` and add the mcp server to the list of `mcpServers`:
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
        "SLACK_MCP_XOXC_TOKEN=$SLACK_MCP_XOXC_TOKEN",
        "-e",
        "SLACK_MCP_XOXD_TOKEN=$SLACK_MCP_XOXD_TOKEN",
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

#### Using npx with `sse` transport:

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
        "Authorization: Bearer ${SLACK_MCP_SSE_API_KEY}"
      ],
      "env": {
        "SLACK_MCP_SSE_API_KEY": "my-$$e-$ecret"
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
        "Authorization: Bearer ${SLACK_MCP_SSE_API_KEY}"
      ],
      "env": {
        "SLACK_MCP_SSE_API_KEY": "my-$$e-$ecret"
      }
    }
  }
}
```
</details>

#### TLS and Exposing to the Internet

There are several reasons why you might need to setup HTTPS for your SSE.
- `mcp-remote` is capable to handle only https schemes;
- it is generally a good practice to use TLS for any service exposed to the internet;

You could use `ngrok`:

```bash
ngrok http 3001
```

and then use the endpoint `https://903d-xxx-xxxx-xxxx-10b4.ngrok-free.app` for your `mcp-remote` argument.

#### Using Docker

For detailed information about all environment variables, see [Environment Variables](https://github.com/korotovsky/slack-mcp-server?tab=readme-ov-file#environment-variables).

```bash
export SLACK_MCP_XOXC_TOKEN=xoxc-...
export SLACK_MCP_XOXD_TOKEN=xoxd-...

docker pull ghcr.io/korotovsky/slack-mcp-server:latest
docker run -i --rm \
  -e SLACK_MCP_XOXC_TOKEN \
  -e SLACK_MCP_XOXD_TOKEN \
  slack-mcp-server --transport stdio
```

Or, the docker-compose way:

```bash
wget -O docker-compose.yml https://github.com/korotovsky/slack-mcp-server/releases/latest/download/docker-compose.yml
wget -O .env https://github.com/korotovsky/slack-mcp-server/releases/latest/download/.env.dist
nano .env # Edit .env file with your tokens from step 1 of the setup guide
docker-compose up -d
```

#### Console Arguments

| Argument              | Required ? | Description                                                              |
|-----------------------|------------|--------------------------------------------------------------------------|
| `--transport` or `-t` | Yes        | Select transport for the MCP Server, possible values are: `stdio`, `sse` |

#### Environment Variables

| Variable                       | Required ? | Default     | Description                                                                   |
|--------------------------------|------------|-------------|-------------------------------------------------------------------------------|
| `SLACK_MCP_XOXC_TOKEN`         | Yes        | `nil`       | Authentication data token field `token` from POST data field-set (`xoxc-...`) |
| `SLACK_MCP_XOXD_TOKEN`         | Yes        | `nil`       | Authentication data token from cookie `d` (`xoxd-...`)                        |
| `SLACK_MCP_SERVER_PORT`        | No         | `3001`      | Port for the MCP server to listen on                                          |
| `SLACK_MCP_SERVER_HOST`        | No         | `127.0.0.1` | Host for the MCP server to listen on                                          |
| `SLACK_MCP_SSE_API_KEY`        | No         | `nil`       | Authorization Bearer token when `transport` is `sse`                          |
| `SLACK_MCP_PROXY`              | No         | `nil`       | Proxy URL for the MCP server to use                                           |
| `SLACK_MCP_SERVER_CA`          | No         | `nil`       | Path to the CA certificate of the trust store                                 |
| `SLACK_MCP_SERVER_CA_INSECURE` | No         | `false`     | Trust all insecure requests (NOT RECOMMENDED)                                 |

### Debugging Tools

```bash
# Run the inspector with stdio transport
npx @modelcontextprotocol/inspector go run mcp/mcp-server.go --transport stdio

# View logs
tail -n 20 -f ~/Library/Logs/Claude/mcp*.log
```

## Security

- Never share API tokens
- Keep .env files secure and private

## License

Licensed under MIT - see [LICENSE](LICENSE) file. This is not an official Slack product.
