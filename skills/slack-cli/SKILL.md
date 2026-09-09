---
name: slack-cli
description: Invoke the `slack-cli` binary to read and act on a Slack workspace from the command line — list channels, read/search conversation history and threads, fetch unread messages, search users, manage user groups, post messages, add reactions, mark channels read, and manage saved items. Use whenever a task needs Slack data or actions, such as "what are the unread messages in #incidents", "search Slack for the deploy thread", "who is @alice", "post a message to #general", "list channels matching X", "react with :rocket: to that message", "what did the team say about Y", or "show replies in this thread". slack-cli is the no-daemon CLI alternative to the slack-mcp-server; it reads a shared on-disk cache so it is cheap to call repeatedly. Output is JSON by default, so results pipe cleanly into `jq`.
---

# Slack CLI

Invoke the `slack-cli` binary (install via `brew install paymog/tap/slack-cli`).
Source of truth is [`paymog/slack-cli`](https://github.com/paymog/slack-cli). It
wraps the `korotovsky/slack-mcp-server` engine for behavior, but **prints JSON by
default** (the underlying MCP server emits CSV) so output pipes cleanly into `jq`.

## Output

Every command prints **JSON by default**. List/table commands (channels,
messages, users, saved items, user groups) emit a JSON array of objects, so pipe
straight into `jq`:

```sh
slack-cli channels list | jq -r '.[].Name'
slack-cli conversations history '#general' --limit 1d | jq -r '.[].Text'
slack-cli users search alice | jq -r '.[].DMChannelID'
```

Field values are strings (CSV carries no types) — use jq's `tonumber` for numeric
comparisons. Write/status commands print a short text or JSON line. `--raw` prints
the underlying CSV/text verbatim.

## Auth (required before any command)

Provide exactly one credential set via env (the CLI also reads stored profiles):

```sh
export SLACK_MCP_XOXP_TOKEN=xoxp-...        # user OAuth — full features (recommended)
# or
export SLACK_MCP_XOXB_TOKEN=xoxb-...        # bot token — invited channels only, no search
# or
export SLACK_MCP_XOXC_TOKEN=xoxc-...        # browser session token  + cookie below
export SLACK_MCP_XOXD_TOKEN=xoxd-...        # browser cookie d (stealth mode)
```

Capability notes:
- **Search** (`conversations search`, `users_search` real-time) and **unreads**
  work best with `xoxp` or browser (`xoxc`/`xoxd`). **Bot tokens cannot search.**
- **Saved items** (`saved …`) require browser tokens (`xoxc`/`xoxd`) only.
- `--govslack` / `SLACK_MCP_GOVSLACK=true` routes to slack-gov.com.

### Stored profiles (alternative to env vars)

```sh
slack-cli auth login [name]        # prompts for mode + token(s); validates before saving
slack-cli auth list                # * marks default
slack-cli auth default <name>
slack-cli --profile <name> <cmd>   # use a profile for one command
slack-cli auth status              # show resolved source + mode
slack-cli auth logout <name> [-f]
```

Precedence: explicit `--xoxp/--xoxc/...` flags or `SLACK_MCP_*` env → `--profile <name>`
→ default profile. Explicit tokens + `--profile` is rejected as ambiguous.
`SLACK_CLI_PROFILE` sets the profile via env.

## Cache (do this first for name lookups)

`#channel-name` / `@username` lookups and `channels list` need a warm cache.
The cache is on disk and shared across every invocation, so refresh once:

```sh
slack-cli cache refresh            # fetch users + channels, write cache to disk
```

Read commands auto-load the on-disk cache (and fetch on first run). Use
`--no-cache` to skip it — then only raw IDs (`C…`, `U…`, `D…`) resolve, not names.

## Channels / IDs

`<channel>` accepts an ID (`C123…`), a name (`#general`), or a DM (`@username`).

## Read commands

```sh
# Channels (JSON array; fields: ID,Name,Topic,Purpose,MemberCount,Cursor)
slack-cli channels list [--types public_channel,private_channel,im,mpim] [--query foo] [--query-targets name,topic,purpose] [--sort popularity] [--limit 100] [--cursor C]
slack-cli channels me                      # channels you belong to

# Conversation history & threads
slack-cli conversations history <channel> [--limit 1d|1w|30d|<count>] [--cursor C] [--activity]
slack-cli conversations replies <channel> <thread_ts>
# Pagination: read the Cursor field of the last element, then pass --limit='' --cursor <value>.
# Unlisted Slack apps: 1 req/min and 15 msgs/page on history/replies. The CLI waits
# Retry-After and shares the slot across processes. Set SLACK_MCP_UNLISTED_HISTORY=1
# to force that cap. Default --timeout is 2m. Do not hammer these in a loop.

# Search (needs xoxp or browser token; not bot)
slack-cli conversations search [query] \
  [--in-channel #general] [--in-dm @user] [--with @user] [--from @user] \
  [--before YYYY-MM-DD] [--after YYYY-MM-DD] [--on YYYY-MM-DD] [--during July] \
  [--threads-only] [--limit 20] [--cursor C]
# A full Slack message URL as the query returns just that message.

# Unreads, prioritized DMs > partner > internal (best with xoxp/browser)
slack-cli conversations unreads [--types all|dm|group_dm|partner|internal] [--mentions-only] [--max-channels 50] [--max-messages-per-channel 10] [--include-muted]

# Users (JSON array incl. DMChannelID for quick messaging)
slack-cli users search <query> [--limit 10]

# User groups
slack-cli usergroups list [--include-users] [--include-disabled]
slack-cli usergroups me <list|join|leave> [--usergroup-id S123]

# Saved items (browser tokens only)
slack-cli saved list [--filter saved|completed|archived] [--limit 50]

# Attachments (download a file by ID; always available, no env var needed).
slack-cli attachments get <file_id> [-o path]   # Fxxxxxxxxxx, max 5MB
```

## Write / sensitive commands (opt-in)

Disabled by default — each needs an env var set in the same invocation, so an
agent never posts or mutates by accident. The allowlist forms (`C123,D456`, or
`!C123` for all-except) restrict which channels are writable.

```sh
SLACK_MCP_ADD_MESSAGE_TOOL=true  slack-cli conversations add <channel> -t "hello" [--thread-ts 123.456] [--content-type text/markdown|text/plain]
SLACK_MCP_ADD_MESSAGE_TOOL=true  slack-cli conversations add <channel> --blocks '<Block Kit JSON array>'
SLACK_MCP_MARK_TOOL=true         slack-cli conversations mark <channel> [--ts 123.456]
SLACK_MCP_REACTION_TOOL=true     slack-cli reactions add <channel> <timestamp> --emoji rocket
SLACK_MCP_REACTION_TOOL=true     slack-cli reactions remove <channel> <timestamp> --emoji rocket
slack-cli usergroups create --name "Eng" [--handle eng] [--description ...] [--channels C1,C2]
slack-cli usergroups update <usergroup_id> [--name ...] [--handle ...] [--channels ...]
slack-cli usergroups users-update <usergroup_id> --users U1,U2,U3
slack-cli saved update <item_id> <ts> [--mark completed] [--date-due <unix>]
slack-cli saved clear-completed
```

## Critical: multi-line / formatted posts

**Default for any multi-line, bulleted, or code-heavy post: use `--blocks` (Block Kit), not `-t`.**

Plain `-t` is fine for one-liners. For anything with newlines, bullets, code fences, or backticks:

1. Prefer `--blocks '<Block Kit JSON array>'` so Slack renders headers/sections/dividers as separate blocks.
2. Pass the payload via an **env var** (or file read into env) — never a shell heredoc, never inline text with backticks.
3. Put real `\n` inside each block's `mrkdwn` text. Do not rely on markdown `-t` preserving newlines through the agent shell.

```sh
# GOOD — Block Kit via env (newlines + backticks survive)
BLOCKS='[{"type":"section","text":{"type":"mrkdwn","text":"line1\n• bullet\n• bullet2"}}]'
SLACK_MCP_ADD_MESSAGE_TOOL=true slack-cli conversations add C123 --thread-ts 123.456 --blocks "$BLOCKS"

# BAD — heredoc / inline markdown with backticks
# Shell treats `...` as command substitution; bullets collapse; partial garbage posts.
SLACK_MCP_ADD_MESSAGE_TOOL=true slack-cli conversations add C123 -t "$(cat <<'EOF'
# title with `code`
• bullet
EOF
)"
```

### Delete a botched post

`slack-cli` has **no delete command**. Use Slack's Web API with the resolved xoxp token:

```sh
TOKEN=$(slack-cli auth token | sed 's/^SLACK_MCP_XOXP_TOKEN=//')
# chat.delete needs channel + message ts (e.g. 1783603079.714919 from replies)
curl -s -X POST https://slack.com/api/chat.delete \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json; charset=utf-8" \
  -d '{"channel":"C061WRT6XM5","ts":"1783603079.714919"}'
```

Only works for messages your token is allowed to delete (your own user messages with `xoxp`, or bot messages with the bot token).

### Verify before walking away

After posting multi-line content, re-read the thread and check for:
- bullets stuck on one line
- missing newlines after headers/code fences
- truncated or shell-error fragments (`command not found`, half-eaten backticks)

If any of those appear, delete via `chat.delete` and repost with `--blocks`.


## Recipes

```sh
# Triage unread DMs and mentions
slack-cli cache refresh
slack-cli conversations unreads --types dm
slack-cli conversations unreads --mentions-only

# Find a thread, then read its replies
slack-cli conversations search "deploy rollback" --in-channel #incidents --after 2024-06-01
slack-cli conversations replies C0123456789 1718000000.123456

# Who is someone, then DM them (needs SLACK_MCP_ADD_MESSAGE_TOOL)
slack-cli users search alice            # note DMChannelID, e.g. D0123
SLACK_MCP_ADD_MESSAGE_TOOL=D0123 slack-cli conversations add D0123 -t "ping"

# Last day of a channel as JSON, extract message text with jq
slack-cli conversations history #general --limit 1d | jq -r '.[].Text'

# Download an image (or any binary) attachment to a file. -o writes the decoded
# bytes and keeps stdout to a small metadata JSON — use it for images/binaries so
# a multi-MB base64 blob doesn't flood the terminal.
slack-cli attachments get F0123ABCD -o avatar.png
# Without -o the bytes come back inline, base64-encoded under .content — decode with:
slack-cli attachments get F0123ABCD | jq -r .content | base64 --decode > avatar.png
```

## Common issues

- **`no Slack credentials`** — set `SLACK_MCP_XOXP_TOKEN` (or xoxb, or xoxc+xoxd)
  or run `slack-cli auth login`.
- **`users cache is not ready` / empty `channels list` / `#name not found`** —
  run `slack-cli cache refresh` first, or pass IDs with `--no-cache`.
- **`conversations_add_message tool is disabled` / reactions / mark disabled** —
  set the matching env var (`SLACK_MCP_ADD_MESSAGE_TOOL`, `SLACK_MCP_REACTION_TOOL`,
  `SLACK_MCP_MARK_TOOL`) in the same command. (`attachments get` needs no env var.)
- **search / saved / unreads return nothing or error** — bot tokens (`xoxb`)
  can't search and lack edge APIs; use `xoxp` or browser tokens. `saved` needs
  browser tokens.
- **slow first run** — the initial `cache refresh` (or first read with no cache)
  crawls the whole workspace; subsequent calls read the cached file.
- **multi-line post looks mangled (bullets on one line / backticks executed)** —
  shell ate the body. Do **not** use heredocs or inline `-t` with backticks for
  multi-line posts. Use `--blocks` + env-var JSON (see **Critical: multi-line /
  formatted posts** above). Delete the bad message with `chat.delete`, then repost.
- **need to delete a message** — no CLI subcommand; call `https://slack.com/api/chat.delete`
  with the xoxp token from `slack-cli auth token` (see recipe above).
