package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mark3labs/mcp-go/mcp"
	"go.uber.org/zap"
)

// Slack drafts (drafts.create, drafts.list, drafts.delete) are only served to a
// browser session token (xoxc + "d" cookie); OAuth tokens get
// not_allowed_token_type. The session comes from SLACK_MCP_XOXC_TOKEN and
// SLACK_MCP_XOXD_TOKEN when the server runs with browser tokens, otherwise from
// the command named in SLACK_MCP_SESSION_CREDS_COMMAND, which must print
// {"xoxc": "...", "d": "..."} on stdout. The session is kept in memory only and
// never logged.
//
// Attached files are uploaded with the same session and no channel, so they
// stay private until the user sends the draft.

const draftsAPIBase = "https://slack.com/api/"

type sessionCreds struct {
	Xoxc  string `json:"xoxc"`
	D     string `json:"d"`
	Error string `json:"error"`
}

var (
	sessionMu     sync.Mutex
	cachedSession *sessionCreds
)

func loadSession(ctx context.Context, refresh bool) (*sessionCreds, error) {
	sessionMu.Lock()
	defer sessionMu.Unlock()
	if cachedSession != nil && !refresh {
		return cachedSession, nil
	}
	if xoxc, xoxd := os.Getenv("SLACK_MCP_XOXC_TOKEN"), os.Getenv("SLACK_MCP_XOXD_TOKEN"); strings.HasPrefix(xoxc, "xoxc-") && xoxd != "" {
		cachedSession = &sessionCreds{Xoxc: xoxc, D: xoxd}
		return cachedSession, nil
	}
	command := strings.TrimSpace(os.Getenv("SLACK_MCP_SESSION_CREDS_COMMAND"))
	if command == "" {
		return nil, errors.New("drafts need a browser session: run with SLACK_MCP_XOXC_TOKEN and SLACK_MCP_XOXD_TOKEN, or set SLACK_MCP_SESSION_CREDS_COMMAND")
	}
	runCtx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	out, err := exec.CommandContext(runCtx, command).Output()
	var creds sessionCreds
	if jsonErr := json.Unmarshal(out, &creds); jsonErr != nil {
		if err != nil {
			return nil, fmt.Errorf("session credentials command failed: %v", err)
		}
		return nil, errors.New("session credentials command returned invalid output")
	}
	if creds.Error != "" {
		return nil, fmt.Errorf("session credentials: %s", creds.Error)
	}
	if !strings.HasPrefix(creds.Xoxc, "xoxc-") || creds.D == "" {
		return nil, errors.New("session credentials command returned no usable session")
	}
	cachedSession = &creds
	return cachedSession, nil
}

func sessionAPICall(ctx context.Context, method string, form url.Values) (map[string]any, error) {
	for attempt := 0; attempt < 2; attempt++ {
		creds, err := loadSession(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		body := url.Values{}
		for key, values := range form {
			body[key] = values
		}
		body.Set("token", creds.Xoxc)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, draftsAPIBase+method, strings.NewReader(body.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Cookie", "d="+creds.D)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s request failed: %w", method, err)
		}
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20))
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: reading response: %w", method, err)
		}
		var result map[string]any
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, fmt.Errorf("%s: HTTP %d, invalid JSON", method, resp.StatusCode)
		}
		if ok, _ := result["ok"].(bool); ok {
			return result, nil
		}
		slackErr, _ := result["error"].(string)
		if attempt == 0 && (slackErr == "invalid_auth" || slackErr == "not_authed" || slackErr == "token_revoked") {
			continue // the desktop session rotated; read it again once
		}
		return nil, fmt.Errorf("%s failed: %s", method, slackErr)
	}
	return nil, fmt.Errorf("%s failed: session rejected", method)
}

// uploadFilePrivately uploads a file with no channel, so it is not shared until
// a draft that references it is sent.
func uploadFilePrivately(ctx context.Context, name string, content []byte) (string, error) {
	ticket, err := sessionAPICall(ctx, "files.getUploadURLExternal", url.Values{
		"filename": {name},
		"length":   {strconv.Itoa(len(content))},
	})
	if err != nil {
		return "", err
	}
	uploadURL, _ := ticket["upload_url"].(string)
	fileID, _ := ticket["file_id"].(string)
	if uploadURL == "" || fileID == "" {
		return "", errors.New("files.getUploadURLExternal returned no upload URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, bytes.NewReader(content))
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("uploading %s: %w", name, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("uploading %s: HTTP %d", name, resp.StatusCode)
	}
	files, _ := json.Marshal([]map[string]string{{"id": fileID, "title": name}})
	if _, err := sessionAPICall(ctx, "files.completeUploadExternal", url.Values{"files": {string(files)}}); err != nil {
		return "", err
	}
	return fileID, nil
}

// ---------- markdown to Slack rich_text ----------

var inlinePattern = regexp.MustCompile(`\*\*(.+?)\*\*|` + "`([^`]+)`" + `|\[([^\]]+)\]\((https?://[^)\s]+)\)`)

func inlineElements(line string) []map[string]any {
	var elements []map[string]any
	last := 0
	for _, m := range inlinePattern.FindAllStringSubmatchIndex(line, -1) {
		if m[0] > last {
			elements = append(elements, map[string]any{"type": "text", "text": line[last:m[0]]})
		}
		switch {
		case m[2] >= 0:
			elements = append(elements, map[string]any{"type": "text", "text": line[m[2]:m[3]], "style": map[string]any{"bold": true}})
		case m[4] >= 0:
			elements = append(elements, map[string]any{"type": "text", "text": line[m[4]:m[5]], "style": map[string]any{"code": true}})
		default:
			elements = append(elements, map[string]any{"type": "link", "text": line[m[6]:m[7]], "url": line[m[8]:m[9]]})
		}
		last = m[1]
	}
	if last < len(line) {
		elements = append(elements, map[string]any{"type": "text", "text": line[last:]})
	}
	return elements
}

var bulletPattern = regexp.MustCompile(`^(\s*)[-*•]\s+(.*)$`)

// markdownToRichText converts a small markdown subset (paragraphs, "- " bullets
// with two-space indents, **bold**, `code`, [text](url)) to one rich_text block.
func markdownToRichText(markdown string) []map[string]any {
	var blocks []map[string]any
	var section []map[string]any
	var list []map[string]any
	listIndent := -1

	appendText := func(text string) {
		if n := len(section); n > 0 {
			if prev, ok := section[n-1]["text"].(string); ok && section[n-1]["type"] == "text" && section[n-1]["style"] == nil {
				section[n-1]["text"] = prev + text
				return
			}
		}
		section = append(section, map[string]any{"type": "text", "text": text})
	}
	flushSection := func() {
		if len(section) == 0 {
			return
		}
		n := len(section) - 1
		if text, ok := section[n]["text"].(string); ok && section[n]["type"] == "text" {
			trimmed := strings.TrimRight(text, "\n")
			if trimmed == "" {
				section = section[:n]
			} else {
				section[n]["text"] = trimmed
			}
		}
		if len(section) > 0 {
			blocks = append(blocks, map[string]any{"type": "rich_text_section", "elements": section})
		}
		section = nil
	}
	flushList := func() {
		if len(list) > 0 {
			blocks = append(blocks, map[string]any{"type": "rich_text_list", "style": "bullet", "indent": listIndent, "elements": list})
		}
		list = nil
		listIndent = -1
	}

	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		if m := bulletPattern.FindStringSubmatch(line); m != nil {
			flushSection()
			indent := len(strings.ReplaceAll(m[1], "\t", "  ")) / 2
			if indent != listIndent {
				flushList()
				listIndent = indent
			}
			list = append(list, map[string]any{"type": "rich_text_section", "elements": inlineElements(m[2])})
			continue
		}
		if strings.TrimSpace(line) == "" {
			if len(list) > 0 {
				flushList()
				continue
			}
			if len(section) > 0 {
				appendText("\n")
			}
			continue
		}
		flushList()
		if len(section) > 0 {
			appendText("\n")
		}
		for _, element := range inlineElements(line) {
			if element["type"] == "text" && element["style"] == nil {
				appendText(element["text"].(string))
			} else {
				section = append(section, element)
			}
		}
	}
	flushList()
	flushSection()
	return []map[string]any{{"type": "rich_text", "elements": blocks}}
}

func richTextPreview(blocks any, limit int) string {
	var b strings.Builder
	var walk func(any)
	walk = func(node any) {
		switch v := node.(type) {
		case []any:
			for _, item := range v {
				walk(item)
			}
		case map[string]any:
			if text, ok := v["text"].(string); ok && (v["type"] == "text" || v["type"] == "link") {
				b.WriteString(text)
			}
			if elements, ok := v["elements"]; ok {
				walk(elements)
				if v["type"] == "rich_text_section" {
					b.WriteString(" ")
				}
			}
		}
	}
	walk(blocks)
	preview := strings.Join(strings.Fields(b.String()), " ")
	if len([]rune(preview)) > limit {
		preview = string([]rune(preview)[:limit]) + "…"
	}
	return preview
}

// ---------- tool handlers ----------

func draftsToolConfig() (string, error) {
	config := os.Getenv("SLACK_MCP_DRAFTS_TOOL")
	if config == "" {
		for _, name := range strings.Split(os.Getenv("SLACK_MCP_ENABLED_TOOLS"), ",") {
			if strings.HasPrefix(strings.TrimSpace(name), "drafts_") {
				return "true", nil
			}
		}
		return "", errors.New("draft tools are disabled; set SLACK_MCP_DRAFTS_TOOL to true or to a comma-separated channel allowlist")
	}
	return config, nil
}

func splitList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// DraftsCreateHandler saves an unsent draft (text plus optional private file
// uploads) for the signed-in user. Nothing is posted.
func (ch *ConversationsHandler) DraftsCreateHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	config, err := draftsToolConfig()
	if err != nil {
		return nil, err
	}
	channel := strings.TrimSpace(request.GetString("channel_id", ""))
	if channel == "" {
		return nil, errors.New("channel_id is required")
	}
	if channel, err = ch.resolveChannelID(ctx, channel); err != nil {
		return nil, err
	}
	if !isChannelAllowedForConfig(channel, config) {
		return nil, fmt.Errorf("drafts_create is not allowed for channel %q by SLACK_MCP_DRAFTS_TOOL", channel)
	}
	text := request.GetString("text", "")
	if strings.TrimSpace(text) == "" {
		return nil, errors.New("text is required")
	}
	threadTS := strings.TrimSpace(request.GetString("thread_ts", ""))
	if threadTS != "" && !strings.Contains(threadTS, ".") {
		return nil, errors.New("thread_ts must be a valid timestamp in format 1234567890.123456")
	}

	// Slack keeps one unsent draft per channel (attached_draft_exists). Check
	// before uploading anything, and replace only the draft named explicitly.
	replaceID := strings.TrimSpace(request.GetString("replace_draft_id", ""))
	existing, err := listActiveDrafts(ctx)
	if err != nil {
		return nil, err
	}
	var replace map[string]any
	for _, draft := range existing {
		channelID, draftThread := draftDestination(draft)
		if channelID != channel {
			continue
		}
		if replaceID != "" && draft["id"] == replaceID {
			replace = draft
			continue
		}
		if threadTS == "" && draftThread == "" && replaceID == "" {
			return nil, fmt.Errorf("channel %s already has an unsent draft %v (%q); pass replace_draft_id to replace it", channel, draft["id"], richTextPreview(draft["blocks"], 80))
		}
	}
	if replaceID != "" && replace == nil {
		return nil, fmt.Errorf("replace_draft_id %q is not an unsent draft in channel %s", replaceID, channel)
	}

	paths := splitList(request.GetString("file_paths", ""))
	fileIDs := splitList(request.GetString("file_ids", ""))
	type uploaded struct {
		ID   string `json:"file_id"`
		Name string `json:"filename"`
		Size int    `json:"size"`
	}
	var uploads []uploaded
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return nil, fmt.Errorf("file path %q must be absolute", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("file %q is not a regular file", path)
		}
		if info.Size() == 0 || info.Size() > maxFileSizeBytes {
			return nil, fmt.Errorf("file %q must be between 1 byte and %d bytes", path, maxFileSizeBytes)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %q: %w", path, err)
		}
		name := filepath.Base(path)
		fileID, err := uploadFilePrivately(ctx, name, content)
		if err != nil {
			return nil, fmt.Errorf("uploading %q: %w", name, err)
		}
		uploads = append(uploads, uploaded{ID: fileID, Name: name, Size: len(content)})
		fileIDs = append(fileIDs, fileID)
	}

	// Check every attachment before touching the existing draft, so a failed
	// create never leaves the channel without its draft.
	for _, id := range fileIDs {
		if _, err := sessionAPICall(ctx, "files.info", url.Values{"file": {id}}); err != nil {
			return nil, fmt.Errorf("file %s is not available (%v); nothing was changed", id, err)
		}
	}

	if replace != nil {
		if err := deleteDraft(ctx, replaceID); err != nil {
			return nil, fmt.Errorf("replacing draft %s: %w (uploaded file IDs, reusable via file_ids: %s)", replaceID, err, strings.Join(fileIDs, ","))
		}
	}

	destination := map[string]any{"channel_id": channel}
	if threadTS != "" {
		destination["thread_ts"] = threadTS
		destination["broadcast"] = request.GetBool("broadcast", false)
	}
	blocksJSON, _ := json.Marshal(markdownToRichText(text))
	if fileIDs == nil {
		fileIDs = []string{}
	}
	fileIDsJSON, _ := json.Marshal(fileIDs)
	destinationsJSON, _ := json.Marshal([]map[string]any{destination})

	result, err := sessionAPICall(ctx, "drafts.create", url.Values{
		"client_msg_id":    {uuid.NewString()},
		"blocks":           {string(blocksJSON)},
		"file_ids":         {string(fileIDsJSON)},
		"destinations":     {string(destinationsJSON)},
		"is_from_composer": {"false"},
		"date_scheduled":   {"0"},
	})
	if err != nil {
		ch.logger.Error("drafts.create failed", zap.String("channel", channel), zap.Error(err))
		if len(fileIDs) > 0 {
			return nil, fmt.Errorf("%w (file IDs, reusable via file_ids: %s)", err, strings.Join(fileIDs, ","))
		}
		return nil, err
	}
	draft, _ := result["draft"].(map[string]any)
	out, err := json.Marshal(map[string]any{
		"replaced_draft": replaceID,
		"draft_id":       draft["id"],
		"channel_id":     channel,
		"thread_ts":      threadTS,
		"file_ids":       fileIDs,
		"uploaded_files": uploads,
		"preview":        richTextPreview(draft["blocks"], 160),
		"status":         "Saved as an unsent draft. Review and send it from Slack's Drafts & sent.",
	})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(out)), nil
}

func listActiveDrafts(ctx context.Context) ([]map[string]any, error) {
	result, err := sessionAPICall(ctx, "drafts.list", url.Values{"is_active": {"true"}})
	if err != nil {
		return nil, err
	}
	items, _ := result["drafts"].([]any)
	var drafts []map[string]any
	for _, item := range items {
		draft, _ := item.(map[string]any)
		if draft == nil || draft["is_sent"] == true || draft["is_deleted"] == true {
			continue
		}
		drafts = append(drafts, draft)
	}
	return drafts, nil
}

func draftDestination(draft map[string]any) (channelID, threadTS string) {
	destinations, _ := draft["destinations"].([]any)
	if len(destinations) > 0 {
		if dest, ok := destinations[0].(map[string]any); ok {
			channelID, _ = dest["channel_id"].(string)
			threadTS, _ = dest["thread_ts"].(string)
		}
	}
	return channelID, threadTS
}

func deleteDraft(ctx context.Context, draftID string) error {
	// Slack rejects deletes whose client timestamp is older than the draft's
	// last update (draft_has_conflict), so send the current time.
	now := strconv.FormatFloat(float64(time.Now().UnixMicro())/1e6, 'f', 6, 64)
	_, err := sessionAPICall(ctx, "drafts.delete", url.Values{
		"draft_id":               {draftID},
		"client_last_updated_ts": {now},
	})
	return err
}

// DraftsListHandler lists the signed-in user's unsent drafts.
func (ch *ConversationsHandler) DraftsListHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := draftsToolConfig(); err != nil {
		return nil, err
	}
	filter := strings.TrimSpace(request.GetString("channel_id", ""))
	if filter != "" {
		var err error
		if filter, err = ch.resolveChannelID(ctx, filter); err != nil {
			return nil, err
		}
	}
	drafts, err := listActiveDrafts(ctx)
	if err != nil {
		return nil, err
	}
	var rows []map[string]any
	for _, draft := range drafts {
		channelID, threadTS := draftDestination(draft)
		if filter != "" && channelID != filter {
			continue
		}
		updated := ""
		if ts, ok := draft["last_updated_ts"].(string); ok {
			if secs, err := strconv.ParseFloat(ts, 64); err == nil {
				updated = time.Unix(int64(secs), 0).UTC().Format(time.RFC3339)
			}
		}
		rows = append(rows, map[string]any{
			"draft_id":     draft["id"],
			"channel_id":   channelID,
			"thread_ts":    threadTS,
			"preview":      richTextPreview(draft["blocks"], 160),
			"file_ids":     draft["file_ids"],
			"last_updated": updated,
		})
	}
	out, err := json.Marshal(map[string]any{"drafts": rows, "count": len(rows)})
	if err != nil {
		return nil, err
	}
	return mcp.NewToolResultText(string(out)), nil
}

// DraftsDeleteHandler deletes one unsent draft. Files attached to it are kept.
func (ch *ConversationsHandler) DraftsDeleteHandler(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if _, err := draftsToolConfig(); err != nil {
		return nil, err
	}
	draftID := strings.TrimSpace(request.GetString("draft_id", ""))
	if !strings.HasPrefix(draftID, "Dr") {
		return nil, errors.New("draft_id is required (from drafts_list)")
	}
	if err := deleteDraft(ctx, draftID); err != nil {
		return nil, err
	}
	out, _ := json.Marshal(map[string]any{"draft_id": draftID, "deleted": true})
	return mcp.NewToolResultText(string(out)), nil
}
