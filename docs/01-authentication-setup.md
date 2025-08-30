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

#### Alternative: Using `SLACK_MCP_XOXP_TOKEN` (User OAuth)

Instead of using browser-based tokens (`xoxc`/`xoxd`), you can use a User OAuth token:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app
2. Under "OAuth & Permissions", add the following scopes:
    - `channels:history` - View messages in public channels
    - `channels:read` - View basic information about public channels
    - `groups:history` - View messages in private channels
    - `groups:read` - View basic information about private channels
    - `im:history` - View messages in direct messages.
    - `im:read` - View basic information about direct messages
    - `im:write` - Start direct messages with people on a user’s behalf (new since `v1.1.18`)
    - `mpim:history` - View messages in group direct messages
    - `mpim:read` - View basic information about group direct messages
    - `mpim:write` - Start group direct messages with people on a user’s behalf (new since `v1.1.18`)
    - `users:read` - View people in a workspace.
    - `chat:write` - Send messages on a user’s behalf. (new since `v1.1.18`)
    - `search:read` - Search a workspace’s content. (new since `v1.1.18`)

3. Install the app to your workspace
4. Copy the "User OAuth Token" (starts with `xoxp-`)

#### Alternative: Using `SLACK_MCP_XOXB_TOKEN` (Bot User OAuth)

Same as User OAuth above, but with these differences:
- Use **Bot Token Scopes** (not User OAuth Scopes)
- Remove: `im:write`, `mpim:write`, `search:read` scopes
- Copy the "Bot User OAuth Token" (starts with `xoxb-`)
- **Important**: Bot must be invited to channels/conversations to access messages

**Limitations**: No search functionality, must be added to private channels/DMs, cannot access Slack Connect users

> **Note**: You only need **one** of the following: XOXB token (Bot User OAuth), XOXP token (User OAuth), **or** both XOXC/XOXD tokens (session-based). Bot tokens are ideal for automated applications but have some limitations compared to user tokens.

See next: [Installation](02-installation.md)
