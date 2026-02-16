### 1. Authentication Setup

> **Maintainer note**
>
> I’m currently seeking a new **full-time or contract engineering role** after losing my primary job.  
> This directly impacts my ability to maintain this project long-term.
>
> If you know a **Hiring Manager, Engineering Manager, or startup team** that might be a good fit, I’d be grateful for an introduction.
>
> 👉 See the full context in **[this issue](https://github.com/korotovsky/slack-mcp-server/issues/150)**  
> 📩 Contact: `dmitry@korotovsky.io`

Open up your Slack in your browser and login.

> **Note**: You only need one of the following: an `xoxp-*` User OAuth token, an `xoxb-*` Bot token, or both `xoxc-*` and `xoxd-*` session tokens. User/Bot tokens are more secure and do not require a browser session. If multiple are provided, priority is `xoxp` > `xoxb` > `xoxc/xoxd`.

#### Option 1: Using `SLACK_MCP_XOXC_TOKEN`/`SLACK_MCP_XOXD_TOKEN` (Browser session)

##### Lookup `SLACK_MCP_XOXC_TOKEN`

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

##### Lookup `SLACK_MCP_XOXD_TOKEN`

- Switch to "Application" tab and select "Cookies" in the left navigation pane.
- Find the cookie with the name `d`.  That's right, just the letter `d`.
- Double-click the Value of this cookie.
- Press Ctrl+C or Cmd+C to copy it's value to clipboard.
- Save it for later.

#### Option 2: Using `SLACK_MCP_XOXP_TOKEN` (User OAuth)

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
    - `chat:write` - Send messages on a user's behalf. (new since `v1.1.18`)
    - `search:read` - Search a workspace's content. (new since `v1.1.18`)
    - `usergroups:read` - View user groups in a workspace.
    - `usergroups:write` - Create and manage user groups.

3. Install the app to your workspace
4. Copy the "User OAuth Token" (starts with `xoxp-`)

##### App manifest (preconfigured scopes)
To create the app from a manifest with permissions preconfigured, use the following code snippet:

```json
{
    "display_information": {
        "name": "Slack MCP"
    },
    "oauth_config": {
        "scopes": {
            "user": [
                "channels:history",
                "channels:read",
                "groups:history",
                "groups:read",
                "im:history",
                "im:read",
                "im:write",
                "mpim:history",
                "mpim:read",
                "mpim:write",
                "users:read",
                "chat:write",
                "search:read",
                "usergroups:read",
                "usergroups:write"
            ]
        }
    },
    "settings": {
        "org_deploy_enabled": false,
        "socket_mode_enabled": false,
        "token_rotation_enabled": false
    }
}
```

#### Option 3: Using `SLACK_MCP_XOXB_TOKEN` (Bot Token)

You can also use a Bot token instead of a User token:

1. Go to [api.slack.com/apps](https://api.slack.com/apps) and create a new app
2. Under "OAuth & Permissions", add Bot Token Scopes (same as User scopes above, except `search:read`)
3. Install the app to your workspace
4. Copy the "Bot User OAuth Token" (starts with `xoxb-`)
5. **Important**: Bot must be invited to channels for access

> **Note**: Bot tokens cannot use `search.messages` API, so `conversations_search_messages` tool will not be available.


See next: [Installation](02-installation.md)
