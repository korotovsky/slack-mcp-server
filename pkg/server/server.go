package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/korotovsky/slack-mcp-server/pkg/handler"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/server/auth"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/korotovsky/slack-mcp-server/pkg/version"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.uber.org/zap"
)

type MCPServer struct {
	server *server.MCPServer
	logger *zap.Logger
}

func NewMCPServer(provider *provider.ApiProvider, logger *zap.Logger) *MCPServer {
	s := server.NewMCPServer(
		"Slack MCP Server",
		version.Version,
		server.WithLogging(),
		server.WithRecovery(),
		server.WithToolHandlerMiddleware(buildLoggerMiddleware(logger)),
		server.WithToolHandlerMiddleware(auth.BuildMiddleware(provider.ServerTransport(), logger)),
	)

	conversationsHandler := handler.NewConversationsHandler(provider, logger)

	s.AddTool(mcp.NewTool("conversations_history",
		mcp.WithDescription("Get messages from the channel (or DM) by channel_id, the last row/column in the response is used as 'cursor' parameter for pagination if not empty"),
		mcp.WithTitleAnnotation("Get Conversation History"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("    - `channel_id` (string): ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithBoolean("include_activity_messages",
			mcp.Description("If true, the response will include activity messages such as 'channel_join' or 'channel_leave'. Default is boolean false."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithString("limit",
			mcp.DefaultString("1d"),
			mcp.Description("Limit of messages to fetch in format of maximum ranges of time (e.g. 1d - 1 day, 1w - 1 week, 30d - 30 days, 90d - 90 days which is a default limit for free tier history) or number of messages (e.g. 50). Must be empty when 'cursor' is provided."),
		),
	), conversationsHandler.ConversationsHistoryHandler)

	s.AddTool(mcp.NewTool("conversations_replies",
		mcp.WithDescription("Get a thread of messages posted to a conversation by channelID and thread_ts, the last row/column in the response is used as 'cursor' parameter for pagination if not empty"),
		mcp.WithTitleAnnotation("Get Thread Replies"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("thread_ts",
			mcp.Required(),
			mcp.Description("Unique identifier of either a thread's parent message or a message in the thread. ts must be the timestamp in format 1234567890.123456 of an existing message with 0 or more replies."),
		),
		mcp.WithBoolean("include_activity_messages",
			mcp.Description("If true, the response will include activity messages such as 'channel_join' or 'channel_leave'. Default is boolean false."),
			mcp.DefaultBool(false),
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithString("limit",
			mcp.DefaultString("1d"),
			mcp.Description("Limit of messages to fetch in format of maximum ranges of time (e.g. 1d - 1 day, 30d - 30 days, 90d - 90 days which is a default limit for free tier history) or number of messages (e.g. 50). Must be empty when 'cursor' is provided."),
		),
	), conversationsHandler.ConversationsRepliesHandler)

	s.AddTool(mcp.NewTool("conversations_add_message",
		mcp.WithDescription("Add a message to a public channel, private channel, or direct message (DM, or IM) conversation by channel_id and thread_ts."),
		mcp.WithTitleAnnotation("Send Message"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("thread_ts",
			mcp.Description("Unique identifier of either a thread's parent message or a message in the thread_ts must be the timestamp in format 1234567890.123456 of an existing message with 0 or more replies. Optional, if not provided the message will be added to the channel itself, otherwise it will be added to the thread."),
		),
		mcp.WithString("text",
			mcp.Description("Message text in specified content_type format. Example: 'Hello, world!' for text/plain or '# Hello, world!' for text/markdown."),
		),
		mcp.WithString("content_type",
			mcp.DefaultString("text/markdown"),
			mcp.Description("Content type of the message. Default is 'text/markdown'. Allowed values: 'text/markdown', 'text/plain'."),
		),
	), conversationsHandler.ConversationsAddMessageHandler)

	s.AddTool(mcp.NewTool("reactions_add",
		mcp.WithDescription("Add an emoji reaction to a message in a public channel, private channel, or direct message (DM, or IM) conversation."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("timestamp",
			mcp.Required(),
			mcp.Description("Timestamp of the message to add reaction to, in format 1234567890.123456."),
		),
		mcp.WithString("emoji",
			mcp.Required(),
			mcp.Description("The name of the emoji to add as a reaction (without colons). Example: 'thumbsup', 'heart', 'rocket'."),
		),
	), conversationsHandler.ReactionsAddHandler)

	s.AddTool(mcp.NewTool("reactions_remove",
		mcp.WithDescription("Remove an emoji reaction from a message in a public channel, private channel, or direct message (DM, or IM) conversation."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("channel_id",
			mcp.Required(),
			mcp.Description("ID of the channel in format Cxxxxxxxxxx or its name starting with #... or @... aka #general or @username_dm."),
		),
		mcp.WithString("timestamp",
			mcp.Required(),
			mcp.Description("Timestamp of the message to remove reaction from, in format 1234567890.123456."),
		),
		mcp.WithString("emoji",
			mcp.Required(),
			mcp.Description("The name of the emoji to remove as a reaction (without colons). Example: 'thumbsup', 'heart', 'rocket'."),
		),
	), conversationsHandler.ReactionsRemoveHandler)

	s.AddTool(mcp.NewTool("attachment_get_data",
		mcp.WithDescription("Download an attachment's content by file ID. Returns file metadata and content (text files as-is, binary files as base64). Maximum file size is 5MB."),
		mcp.WithTitleAnnotation("Get Attachment Data"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("file_id",
			mcp.Required(),
			mcp.Description("The ID of the attachment to download, in format Fxxxxxxxxxx. Attachment IDs can be found in message metadata when HasMedia is true or AttachmentCount > 0."),
		),
	), conversationsHandler.FilesGetHandler)

	conversationsSearchTool := mcp.NewTool("conversations_search_messages",
		mcp.WithDescription("Search messages in a public channel, private channel, or direct message (DM, or IM) conversation using filters. All filters are optional, if not provided then search_query is required."),
		mcp.WithTitleAnnotation("Search Messages"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("search_query",
			mcp.Description("Search query to filter messages. Example: 'marketing report' or full URL of Slack message e.g. 'https://slack.com/archives/C1234567890/p1234567890123456', then the tool will return a single message matching given URL, herewith all other parameters will be ignored."),
		),
		mcp.WithString("filter_in_channel",
			mcp.Description("Filter messages in a specific public/private channel by its ID or name. Example: 'C1234567890', 'G1234567890', or '#general'. If not provided, all channels will be searched."),
		),
		mcp.WithString("filter_in_im_or_mpim",
			mcp.Description("Filter messages in a direct message (DM) or multi-person direct message (MPIM) conversation by its ID or name. Example: 'D1234567890' or '@username_dm'. If not provided, all DMs and MPIMs will be searched."),
		),
		mcp.WithString("filter_users_with",
			mcp.Description("Filter messages with a specific user by their ID or display name in threads and DMs. Example: 'U1234567890' or '@username'. If not provided, all threads and DMs will be searched."),
		),
		mcp.WithString("filter_users_from",
			mcp.Description("Filter messages from a specific user by their ID or display name. Example: 'U1234567890' or '@username'. If not provided, all users will be searched."),
		),
		mcp.WithString("filter_date_before",
			mcp.Description("Filter messages sent before a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_after",
			mcp.Description("Filter messages sent after a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_on",
			mcp.Description("Filter messages sent on a specific date in format 'YYYY-MM-DD'. Example: '2023-10-01', 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithString("filter_date_during",
			mcp.Description("Filter messages sent during a specific period in format 'YYYY-MM-DD'. Example: 'July', 'Yesterday' or 'Today'. If not provided, all dates will be searched."),
		),
		mcp.WithBoolean("filter_threads_only",
			mcp.Description("If true, the response will include only messages from threads. Default is boolean false."),
		),
		mcp.WithString("cursor",
			mcp.DefaultString(""),
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
		mcp.WithNumber("limit",
			mcp.DefaultNumber(20),
			mcp.Description("The maximum number of items to return. Must be an integer between 1 and 100."),
		),
	)
	// Only register search tool for non-bot tokens (bot tokens cannot use search.messages API)
	if !provider.IsBotToken() {
		s.AddTool(conversationsSearchTool, conversationsHandler.ConversationsSearchHandler)
	}

	s.AddTool(mcp.NewTool("users_search",
		mcp.WithDescription("Search for users by name, email, or display name. Returns user details and DM channel ID if available."),
		mcp.WithTitleAnnotation("Search Users"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query - matches against real name, display name, username, or email."),
		),
		mcp.WithNumber("limit",
			mcp.DefaultNumber(10),
			mcp.Description("Maximum number of results to return (1-100). Default is 10."),
		),
	), conversationsHandler.UsersSearchHandler)

	channelsHandler := handler.NewChannelsHandler(provider, logger)
	usergroupsHandler := handler.NewUsergroupsHandler(provider, logger)

	s.AddTool(mcp.NewTool("channels_list",
		mcp.WithDescription("Get list of channels"),
		mcp.WithTitleAnnotation("List Channels"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("channel_types",
			mcp.Required(),
			mcp.Description("Comma-separated channel types. Allowed values: 'mpim', 'im', 'public_channel', 'private_channel'. Example: 'public_channel,private_channel,im'"),
		),
		mcp.WithString("sort",
			mcp.Description("Type of sorting. Allowed values: 'popularity' - sort by number of members/participants in each channel."),
		),
		mcp.WithNumber("limit",
			mcp.DefaultNumber(100),
			mcp.Description("The maximum number of items to return. Must be an integer between 1 and 1000 (maximum 999)."), // context fix for cursor: https://github.com/korotovsky/slack-mcp-server/issues/7
		),
		mcp.WithString("cursor",
			mcp.Description("Cursor for pagination. Use the value of the last row and column in the response as next_cursor field returned from the previous request."),
		),
	), channelsHandler.ChannelsHandler)

	// User groups tools
	s.AddTool(mcp.NewTool("usergroups_list",
		mcp.WithDescription("List all user groups (subteams) in the Slack workspace. User groups are mention groups like @engineering or @design that notify all members. Use this to discover available groups, check group membership counts, or find a group's ID before joining/updating it. Returns CSV with columns: id, name, handle, description, user_count, is_external."),
		mcp.WithTitleAnnotation("List User Groups"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithBoolean("include_users",
			mcp.Description("Include list of user IDs in each group. Default is false."),
			mcp.DefaultBool(false),
		),
		mcp.WithBoolean("include_count",
			mcp.Description("Include user count for each group. Default is true."),
			mcp.DefaultBool(true),
		),
		mcp.WithBoolean("include_disabled",
			mcp.Description("Include disabled/archived groups. Default is false."),
			mcp.DefaultBool(false),
		),
	), usergroupsHandler.UsergroupsListHandler)

	s.AddTool(mcp.NewTool("usergroups_me",
		mcp.WithDescription("Manage your own user group membership. Use action='list' to see which groups you belong to. Use action='join' with a usergroup_id to add yourself to a group (e.g., to receive @mentions). Use action='leave' with a usergroup_id to remove yourself. This is the easiest way to join/leave groups without needing to know the full member list."),
		mcp.WithTitleAnnotation("My User Groups"),
		mcp.WithString("action",
			mcp.Required(),
			mcp.Description("Action to perform: 'list' returns CSV of groups you're a member of, 'join' adds you to a group, 'leave' removes you from a group."),
		),
		mcp.WithString("usergroup_id",
			mcp.Description("ID of the user group (starts with 'S', e.g., 'S0123456789'). Required for 'join' and 'leave' actions. Get IDs from usergroups_list."),
		),
	), usergroupsHandler.UsergroupsMeHandler)

	s.AddTool(mcp.NewTool("usergroups_create",
		mcp.WithDescription("Create a new user group (mention group) in the Slack workspace. After creation, use usergroups_users_update to add members, or users can join themselves with usergroups_me. The handle becomes the @mention (e.g., handle='engineering' creates @engineering)."),
		mcp.WithTitleAnnotation("Create User Group"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("name",
			mcp.Required(),
			mcp.Description("Display name of the user group (e.g., 'Engineering Team', 'Design Squad')."),
		),
		mcp.WithString("handle",
			mcp.Description("The @mention handle without the @ symbol (e.g., 'engineering' for @engineering). Keep it short and lowercase. If omitted, Slack auto-generates one from the name."),
		),
		mcp.WithString("description",
			mcp.Description("Purpose or description shown in group details (e.g., 'Backend and frontend engineers')."),
		),
		mcp.WithString("channels",
			mcp.Description("Comma-separated channel IDs where this group is commonly mentioned. Members get suggestions to join these channels."),
		),
	), usergroupsHandler.UsergroupsCreateHandler)

	s.AddTool(mcp.NewTool("usergroups_update",
		mcp.WithDescription("Update a user group's metadata: name, handle (@mention), description, or default channels. Does NOT change members - use usergroups_users_update for that. At least one field must be provided."),
		mcp.WithTitleAnnotation("Update User Group"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("usergroup_id",
			mcp.Required(),
			mcp.Description("ID of the user group to update (starts with 'S', e.g., 'S0123456789'). Get IDs from usergroups_list."),
		),
		mcp.WithString("name",
			mcp.Description("New display name for the group."),
		),
		mcp.WithString("handle",
			mcp.Description("New @mention handle (without @). Changing this changes how users mention the group."),
		),
		mcp.WithString("description",
			mcp.Description("New description for the group."),
		),
		mcp.WithString("channels",
			mcp.Description("New default channel IDs (comma-separated). Replaces existing default channels."),
		),
	), usergroupsHandler.UsergroupsUpdateHandler)

	s.AddTool(mcp.NewTool("usergroups_users_update",
		mcp.WithDescription("Replace all members of a user group with a new list. WARNING: This completely replaces the member list - any user not in the 'users' parameter will be removed. To add/remove just yourself, use usergroups_me instead. To add a single user without removing others, first get current members from usergroups_list with include_users=true, then call this with the combined list."),
		mcp.WithTitleAnnotation("Update User Group Members"),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("usergroup_id",
			mcp.Required(),
			mcp.Description("ID of the user group (starts with 'S', e.g., 'S0123456789'). Get IDs from usergroups_list."),
		),
		mcp.WithString("users",
			mcp.Required(),
			mcp.Description("Comma-separated user IDs that will become the COMPLETE member list (e.g., 'U0123456789,U9876543210'). All current members not in this list will be removed."),
		),
	), usergroupsHandler.UsergroupsUsersUpdateHandler)

	logger.Info("Authenticating with Slack API...",
		zap.String("context", "console"),
	)
	ar, err := provider.Slack().AuthTest()
	if err != nil {
		logger.Fatal("Failed to authenticate with Slack",
			zap.String("context", "console"),
			zap.Error(err),
		)
	}

	logger.Info("Successfully authenticated with Slack",
		zap.String("context", "console"),
		zap.String("team", ar.Team),
		zap.String("user", ar.User),
		zap.String("enterprise", ar.EnterpriseID),
		zap.String("url", ar.URL),
	)

	ws, err := text.Workspace(ar.URL)
	if err != nil {
		logger.Fatal("Failed to parse workspace from URL",
			zap.String("context", "console"),
			zap.String("url", ar.URL),
			zap.Error(err),
		)
	}

	s.AddResource(mcp.NewResource(
		"slack://"+ws+"/channels",
		"Directory of Slack channels",
		mcp.WithResourceDescription("This resource provides a directory of Slack channels."),
		mcp.WithMIMEType("text/csv"),
	), channelsHandler.ChannelsResource)

	s.AddResource(mcp.NewResource(
		"slack://"+ws+"/users",
		"Directory of Slack users",
		mcp.WithResourceDescription("This resource provides a directory of Slack users."),
		mcp.WithMIMEType("text/csv"),
	), conversationsHandler.UsersResource)

	return &MCPServer{
		server: s,
		logger: logger,
	}
}

func (s *MCPServer) ServeSSE(addr string) *server.SSEServer {
	s.logger.Info("Creating SSE server",
		zap.String("context", "console"),
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
		zap.String("address", addr),
	)
	return server.NewSSEServer(s.server,
		server.WithBaseURL(fmt.Sprintf("http://%s", addr)),
		server.WithSSEContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = auth.AuthFromRequest(s.logger)(ctx, r)

			return ctx
		}),
	)
}

func (s *MCPServer) ServeHTTP(addr string) *server.StreamableHTTPServer {
	s.logger.Info("Creating HTTP server",
		zap.String("context", "console"),
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
		zap.String("address", addr),
	)
	return server.NewStreamableHTTPServer(s.server,
		server.WithEndpointPath("/mcp"),
		server.WithHTTPContextFunc(func(ctx context.Context, r *http.Request) context.Context {
			ctx = auth.AuthFromRequest(s.logger)(ctx, r)

			return ctx
		}),
	)
}

func (s *MCPServer) ServeStdio() error {
	s.logger.Info("Starting STDIO server",
		zap.String("version", version.Version),
		zap.String("build_time", version.BuildTime),
		zap.String("commit_hash", version.CommitHash),
	)
	err := server.ServeStdio(s.server)
	if err != nil {
		s.logger.Error("STDIO server error", zap.Error(err))
	}
	return err
}

func buildLoggerMiddleware(logger *zap.Logger) server.ToolHandlerMiddleware {
	return func(next server.ToolHandlerFunc) server.ToolHandlerFunc {
		return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			logger.Info("Request received",
				zap.String("tool", req.Params.Name),
				zap.Any("params", req.Params),
			)

			startTime := time.Now()

			res, err := next(ctx, req)

			duration := time.Since(startTime)

			logger.Info("Request finished",
				zap.String("tool", req.Params.Name),
				zap.Duration("duration", duration),
			)

			return res, err
		}
	}
}
