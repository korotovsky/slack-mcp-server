# Slack MCP Server

Model Context Protocol (MCP) server for Slack Workspaces. This integration supports both Stdio and SSE transports, proxy settings and does not require any permissions or bots being created or approved by Workspace admins 😏.

## Purpose of this Project

This repository contains the implementation of the Model Context Protocol (MCP) tailored for Slack.

### What is MCP in this context?

MCP, in this context, stands for Model Context Protocol. It is a system designed to allow AI models and tools to interact with a user's Slack workspace. This interaction is achieved by providing the AI with the necessary context and capabilities to understand and participate in Slack conversations.

### Primary Goal

The primary goal of this project is to enable AI models and tools to seamlessly integrate with a user's Slack environment. This allows the AI to:

*   Read messages and understand conversations.
*   Respond to queries and participate in discussions.
*   Perform actions within Slack on behalf of the user.
*   Access and process information available in the Slack workspace.

Essentially, MCP aims to bridge the gap between advanced AI capabilities and the collaborative environment of Slack.

### Unique Aspect: User-Specific Tokens

A key and unique aspect of this MCP implementation is its reliance on user-specific tokens. Instead of requiring official Slack bot integration, which often involves administrative approvals and workspace-level permissions, this system operates using the authenticated user's own credentials.

This approach offers several advantages:

*   **Ease of Use:** Users can quickly set up and authorize the AI to interact with their Slack workspace without needing admin rights or lengthy approval processes.
*   **Personalized Interaction:** The AI operates within the context of the specific user, ensuring that its actions and responses are aligned with the user's permissions and persona.
*   **Granular Control:** Users have direct control over the AI's access and can revoke it at any time.

By leveraging user-specific tokens, this project provides a more agile and user-centric way for AI to engage with Slack, bypassing some of the traditional hurdles associated with bot integrations.

## High-Level Architecture

This Slack MCP Server is a Go-based application designed for robustness and efficiency. Its architecture comprises several key components:

*   **Command-Line Interface (CLI):** Provides the entry point for the application. It handles parsing of command-line arguments, allowing users to configure essential settings such as the transport mechanism (e.g., Stdio, SSE) and other operational parameters.
*   **Core Server Logic:** This is the heart of the application, responsible for implementing the Model Context Protocol (MCP). It manages the lifecycle of MCP requests, handles communication with the AI model/tool, and orchestrates the overall workflow.
*   **Slack Provider:** This component acts as an abstraction layer between the core server logic and the Slack API. It encapsulates all Slack-specific communication, including authentication, API calls for fetching data (messages, channels, users), and sending messages. It utilizes the user-specific tokens for these interactions.
*   **Tool Handlers:** For each specific action or tool exposed via MCP (e.g., `conversations_history`, `channels_list`), there's a dedicated handler. These handlers are responsible for processing the incoming MCP request for a particular tool, interacting with the Slack Provider to fetch the necessary data or perform actions, and formatting the response according to the MCP specification.

This modular design allows for clear separation of concerns, making the system easier to maintain, extend, and test.

## Key Architectural Details

This section delves deeper into the core components, mechanisms, and operational aspects of the Slack MCP Server.

### Transport Methods

The server supports two primary transport methods for communication with AI models/tools:

*   **`stdio` (Standard Input/Output):** This method is primarily designed for local CLI interactions. The server reads MCP requests from standard input and writes MCP responses to standard output. This is often used for direct integration with local applications, such as Claude Desktop, where the AI tool can spawn the server process and communicate with it directly.
*   **`SSE` (Server-Sent Events):** This method enables web-based and remote interactions. The server exposes an HTTP endpoint (e.g., `/sse`) that streams MCP responses as Server-Sent Events. This is suitable for scenarios where the AI tool or a proxy (like `mcp-remote`) connects to the server over a network. For security, the SSE transport can be protected with an API key (`SLACK_MCP_SSE_API_KEY`) which must be provided as a Bearer token in the Authorization header. Using TLS encryption is highly recommended when exposing the SSE endpoint over the internet (e.g., via `ngrok` or a reverse proxy).

### Authentication with Slack

The server interacts with Slack on behalf of the user. It achieves this using user-specific tokens that are obtained by inspecting an active Slack session in a web browser:

*   `SLACK_MCP_XOXC_TOKEN`: A token that grants access to Slack's client API.
*   `SLACK_MCP_XOXD_TOKEN`: A session cookie that complements the xoxc token.

These tokens are provided to the server via environment variables. By using these tokens, the server effectively "acts as the user," meaning all actions performed (like reading channels or messages) are done within the permissions scope of the user who provided the tokens. This method bypasses the need for official Slack app installations, bot users, or admin approvals, offering a direct line of interaction.

### Core Logic Breakdown (`pkg` directory)

The Go application's core logic is organized within the `pkg` directory, promoting modularity:

*   **`pkg/provider` (`provider/api.go`):** This package is responsible for all direct interactions with the Slack API. It abstracts the complexities of Slack's Web API, handles authentication using the provided `xoxc` and `xoxd` tokens, constructs API requests, and parses responses. Its primary role is to supply data to the handlers.
*   **`pkg/server` (`server/server.go`, `server/sse_auth.go`):** This package contains the main operational logic for the MCP server. It initializes and runs the server based on the chosen transport method (`stdio` or `SSE`). It's responsible for listening for incoming MCP requests, dispatching them to the appropriate tool handlers, and sending back the responses. The `sse_auth.go` sub-component specifically manages API key authentication for the SSE transport.
*   **`pkg/handler` (`handler/channels.go`, `handler/conversations.go`):** This package houses the specific logic for each MCP tool/action. For example, `channels.go` likely implements the `channels_list` tool, fetching channel information via the provider. Similarly, `conversations.go` would implement `conversations_history` for retrieving messages. Each handler processes the tool-specific parameters and interacts with the `pkg/provider` to get the data from Slack.
*   **`pkg/transport` (`transport/transport.go`):** This package likely defines common interfaces, data structures, and utilities related to the different transport mechanisms (stdio and SSE). It helps in standardizing how data is exchanged regardless of the chosen transport.
*   **`pkg/text` (`text/text_processor.go`):** This package probably includes utilities for processing or formatting text data, which could be used for cleaning up Slack message content or preparing it for the MCP response.
*   **`pkg/version` (`version/version.go`):** This package manages the application's version information. It typically provides a way to embed version details at compile time and expose it, for example, via a command-line flag or an MCP endpoint.

### Configuration

The server's behavior is configured through a combination of command-line arguments and environment variables:

*   **Command-Line Arguments:**
    *   `--transport` (`-t`): The primary argument to select the communication mode (`stdio` or `sse`).
*   **Environment Variables:**
    *   `SLACK_MCP_XOXC_TOKEN` (required): User's Slack client API token.
    *   `SLACK_MCP_XOXD_TOKEN` (required): User's Slack session cookie.
    *   `SLACK_MCP_SERVER_PORT`: Port for the SSE server (default: `3001`).
    *   `SLACK_MCP_SERVER_HOST`: Host address for the SSE server (default: `127.0.0.1`).
    *   `SLACK_MCP_SSE_API_KEY`: Optional API key for securing the SSE transport.
    *   `SLACK_MCP_PROXY`: Optional proxy URL for Slack API requests.
    *   `SLACK_MCP_SERVER_CA`: Path to a custom CA certificate for TLS.
    *   `SLACK_MCP_SERVER_CA_INSECURE`: Boolean to trust insecure server certificates (not recommended).

This dual approach provides flexibility for different deployment scenarios.

### Distribution Methods

The Slack MCP Server can be run in several ways:

*   **From Go Source:** Users can compile and run the server directly from the source code using `go run mcp/mcp-server.go` (assuming `mcp/mcp-server.go` is the main entry point, as suggested by debug snippets). This is common for development or custom builds.
*   **Docker Containerization:** The project supports Docker, with images available from `ghcr.io/korotovsky/slack-mcp-server`. A `Dockerfile` is likely present in the repository for building custom images, and a `docker-compose.yml` is provided for easier orchestration of the server, especially when using the SSE transport. Docker is a convenient way to run the server in an isolated environment with all dependencies included.
*   **NPM Package:** The server is also distributed as an NPM package, installable/runnable via `npx slack-mcp-server@latest`. This method typically bundles pre-compiled binaries for various platforms, making it easy for users (especially those in the Node.js ecosystem or using tools like Claude Desktop) to use the server without needing a Go development environment. The `mcp-remote` tool, also available via `npx`, is used in conjunction with the SSE transport.

## Typical Workflow

The following steps outline a typical workflow for using the Slack MCP Server:

1.  **Setup and Configuration:**
    *   The user first obtains their `SLACK_MCP_XOXC_TOKEN` and `SLACK_MCP_XOXD_TOKEN` by inspecting their active Slack session in a web browser (as detailed in the "Authentication Setup" section).
    *   These tokens are then set as environment variables for the `slack-mcp-server`.
    *   The user chooses a transport method (`stdio` or `SSE`) and configures any other relevant settings (like port for SSE, API key, proxy) via command-line arguments or environment variables.

2.  **Server Start:**
    *   The user starts the `slack-mcp-server` using one of the distribution methods (e.g., `npx slack-mcp-server@latest --transport stdio`, `docker run ...`, or `go run mcp/mcp-server.go --transport stdio`).

3.  **Client Connection:**
    *   An MCP client (which could be an AI model, a plugin like the one for Claude Desktop, or a custom script) connects to the running `slack-mcp-server`.
    *   If `stdio` transport is used, the client typically spawns the server process and communicates via its standard input/output.
    *   If `SSE` transport is used, the client connects to the server's HTTP endpoint (e.g., `http://localhost:3001/sse`). This might involve using a tool like `mcp-remote` to act as a proxy or bridge, especially if the client doesn't natively support MCP over SSE or requires specific headers (like the Authorization Bearer token for `SLACK_MCP_SSE_API_KEY`).

4.  **MCP Request:**
    *   The client sends an MCP request to the server. This request specifies the desired tool and any necessary parameters. For example, a request for the `conversations_history` tool would include the `channel_id` and potentially `limit` or `cursor` values.

5.  **Server-Side Processing:**
    *   The `slack-mcp-server` (specifically the core logic in `pkg/server`) receives the incoming MCP request.
    *   It identifies the requested tool and invokes the corresponding handler from `pkg/handler` (e.g., the handler for `conversations_history`).
    *   The handler then calls functions within the `pkg/provider` (Slack provider) to interact with Slack, passing along any parameters from the request.

6.  **Slack API Interaction:**
    *   The Slack provider (`pkg/provider`) constructs the necessary API calls to the official Slack API.
    *   It uses the user's `SLACK_MCP_XOXC_TOKEN` and `SLACK_MCP_XOXD_TOKEN` for authentication, ensuring all operations are performed within the user's permissions.

7.  **Response Generation:**
    *   The Slack provider receives the raw data from the Slack API (e.g., a list of messages, channel details).
    *   This data is returned to the handler, which then processes and formats it into a structured MCP response. This might involve selecting relevant fields, handling pagination cursors, or using utilities from `pkg/text`.

8.  **MCP Response:**
    *   The server sends the generated MCP response back to the client over the established transport (stdio or SSE).

9.  **Client Action:**
    *   The client receives the MCP response.
    *   It parses the response and uses the information as needed. For example, it might display messages to a user, feed conversation history into an AI model as context for generating a response, or update its list of available channels.

This cycle repeats for each tool interaction the client initiates.

### Feature Demo

![ezgif-316311ee04f444](https://github.com/user-attachments/assets/35dc9895-e695-4e56-acdc-1a46d6520ba0)

## Tools

1. `conversations_history`
  - Get messages from the channel by channelID
  - Required inputs:
    - `channel_id` (string): ID of the channel in format Cxxxxxxxxxx.
    - `cursor` (string): Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request.
    - `limit` (string, default: 28): Limit of messages to fetch.
  - Returns: List of messages with timestamps, user IDs, and text content

2. `channels_list`
  - Get list of channels
  - Required inputs:
    - `channel_types` (string): Comma-separated channel types. Allowed values: 'mpim', 'im', 'public_channel', 'private_channel'. Example: 'public_channel,private_channel,im'.
    - `sort` (string): Type of sorting. Allowed values: 'popularity' - sort by number of members/participants in each channel.
    - `limit` (number, default: 100): Limit of channels to fetch.
    - `cursor` (string): Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request.
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

If you have npm installed, this is the fastest way to get started with `slack-mcp-server` on Claude Desktop.

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
wget -O .env https://github.com/korotovsky/slack-mcp-server/releases/latest/download/default.env.dist
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
