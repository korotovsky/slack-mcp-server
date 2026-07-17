package handler

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/korotovsky/slack-mcp-server/pkg/test/util"
	"github.com/korotovsky/slack-mcp-server/pkg/text"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestIntegrationConversations(t *testing.T) {
	sseKey := uuid.New().String()
	require.NotEmpty(t, sseKey, "sseKey must be generated for integration tests")
	apiKey := os.Getenv("SLACK_MCP_OPENAI_API")
	require.NotEmpty(t, apiKey, "SLACK_MCP_OPENAI_API must be set for integration tests")

	cfg := util.MCPConfig{
		SSEKey:             sseKey,
		MessageToolEnabled: true,
		MessageToolMark:    true,
	}

	mcp, err := util.SetupMCP(cfg)
	if err != nil {
		t.Fatalf("Failed to set up MCP server: %v", err)
	}
	fwd, err := util.SetupForwarding(context.Background(), "http://"+mcp.Host+":"+strconv.Itoa(mcp.Port))
	if err != nil {
		t.Fatalf("Failed to set up ngrok forwarding: %v", err)
	}
	defer fwd.Shutdown()
	defer mcp.Shutdown()

	client := openai.NewClient(option.WithAPIKey(apiKey))
	ctx := context.Background()

	type matchingRule struct {
		csvFieldName    string
		csvFieldValueRE string
		RowPosition     *int
		TotalRows       *int
	}

	type tc struct {
		name                            string
		input                           string
		expectedToolName                string
		expectedToolOutputMatchingRules []matchingRule
		expectedLLMOutputMatchingRules  []string
	}

	cases := []tc{
		{
			name:             "Test conversations_history tool",
			input:            "Provide a list of slack messages from #testcase-1",
			expectedToolName: "conversations_history",
			expectedToolOutputMatchingRules: []matchingRule{
				{
					csvFieldName:    "Text",
					csvFieldValueRE: "^message 3$",
				},
				{
					csvFieldName:    "Text",
					csvFieldValueRE: "^message 2$",
				},
				{
					csvFieldName:    "Text",
					csvFieldValueRE: "^message 1$",
				},
			},
			expectedLLMOutputMatchingRules: []string{
				"message 1", "message 2", "message 3",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := responses.ResponseNewParams{
				Model: "gpt-5.4-mini",
				Tools: []responses.ToolUnionParam{
					{
						OfMcp: &responses.ToolMcpParam{
							ServerLabel: "slack-mcp-server",
							ServerURL:   fmt.Sprintf("%s://%s/sse", fwd.URL.Scheme, fwd.URL.Host),
							RequireApproval: responses.ToolMcpRequireApprovalUnionParam{
								OfMcpToolApprovalSetting: param.NewOpt("never"),
							},
							Headers: map[string]string{
								"Authorization": "Bearer " + sseKey,
							},
						},
					},
				},
				Input: responses.ResponseNewParamsInputUnion{
					OfString: openai.String(tc.input),
				},
			}

			resp, err := client.Responses.New(ctx, params)
			require.NoError(t, err, "API call failed")

			assert.NotNil(t, resp.Status, "completed")

			var llmOutput strings.Builder
			var toolOutput strings.Builder
			for _, out := range resp.Output {
				if out.Type == "message" && out.Role == "assistant" {
					for _, c := range out.Content {
						if c.Type == "output_text" {
							llmOutput.WriteString(c.Text)
						}
					}
				}
				if out.Type == "mcp_call" && out.Name == tc.expectedToolName {
					toolOutput.WriteString(out.Output)
				}
			}

			require.NotEmpty(t, toolOutput, "no tool output captured")

			// Parse CSV
			reader := csv.NewReader(strings.NewReader(toolOutput.String()))
			rows, err := reader.ReadAll()
			require.NoError(t, err, "failed to parse CSV")

			header := rows[0]
			dataRows := rows[1:]
			colIndex := map[string]int{}
			for i, col := range header {
				colIndex[col] = i
			}

			for _, rule := range tc.expectedToolOutputMatchingRules {
				if rule.TotalRows != nil && *rule.TotalRows > 0 {
					assert.Equalf(t, *rule.TotalRows, len(dataRows),
						"expected %d data rows, got %d", rule.TotalRows, len(dataRows))
				}

				idx, ok := colIndex[rule.csvFieldName]
				require.Truef(t, ok, "CSV did not contain column %q, toolOutput: %q", rule.csvFieldName, toolOutput.String())

				re, err := regexp.Compile(rule.csvFieldValueRE)
				require.NoErrorf(t, err, "invalid regex %q", rule.csvFieldValueRE)

				if rule.RowPosition != nil && *rule.RowPosition >= 0 {
					require.Lessf(t, rule.RowPosition, len(dataRows), "RowPosition %d out of range (only %d data rows)", rule.RowPosition, len(dataRows))
					value := dataRows[*rule.RowPosition][idx]
					assert.Regexpf(t, re, value, "row %d, column %q: expected to match %q, got %q",
						rule.RowPosition, rule.csvFieldName, rule.csvFieldValueRE, value)
					continue
				}

				found := false
				for _, row := range dataRows {
					if idx < len(row) && re.MatchString(row[idx]) {
						found = true
						break
					}
				}
				assert.Truef(t, found, "no row in column %q matched %q; full CSV:\n%s",
					rule.csvFieldName, rule.csvFieldValueRE, toolOutput.String())
			}

			for _, pattern := range tc.expectedLLMOutputMatchingRules {
				re, err := regexp.Compile(pattern)
				require.NoErrorf(t, err, "invalid LLM regex %q", pattern)
				assert.Regexpf(t, re, llmOutput.String(), "LLM output did not match regex %q; output:\n%s",
					pattern, llmOutput.String())
			}
		})
	}
}

func TestUnitParseFlexibleDate(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantDate string
		wantErr  bool
	}{
		// Standard formats (existing)
		{
			name:     "YYYY-MM-DD",
			input:    "2025-07-15",
			wantDate: "2025-07-15",
			wantErr:  false,
		},
		{
			name:     "YYYY/MM/DD",
			input:    "2025/07/15",
			wantDate: "2025-07-15",
			wantErr:  false,
		},

		// New flexible month-year formats
		{
			name:     "Month Year - July 2025",
			input:    "July 2025",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Year Month - 2025 July",
			input:    "2025 July",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Abbreviated Month Year - Jul 2025",
			input:    "Jul 2025",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Year Abbreviated Month - 2025 Jul",
			input:    "2025 Jul",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Case insensitive - july 2025",
			input:    "july 2025",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Case insensitive - JULY 2025",
			input:    "JULY 2025",
			wantDate: "2025-07-01",
			wantErr:  false,
		},

		// Day-Month-Year formats
		{
			name:     "1-July-2025",
			input:    "1-July-2025",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "July-25-2025",
			input:    "July-25-2025",
			wantDate: "2025-07-25",
			wantErr:  false,
		},
		{
			name:     "July 10 2025",
			input:    "July 10 2025",
			wantDate: "2025-07-10",
			wantErr:  false,
		},
		{
			name:     "10 July 2025",
			input:    "10 July 2025",
			wantDate: "2025-07-10",
			wantErr:  false,
		},
		{
			name:     "31-December-2025",
			input:    "31-December-2025",
			wantDate: "2025-12-31",
			wantErr:  false,
		},
		{
			name:     "2025 July 10",
			input:    "2025 July 10",
			wantDate: "2025-07-10",
			wantErr:  false,
		},

		// Various month names
		{
			name:     "January full name",
			input:    "January 2025",
			wantDate: "2025-01-01",
			wantErr:  false,
		},
		{
			name:     "February abbreviated",
			input:    "Feb 2025",
			wantDate: "2025-02-01",
			wantErr:  false,
		},
		{
			name:     "September with Sept abbreviation",
			input:    "Sept 2025",
			wantDate: "2025-09-01",
			wantErr:  false,
		},

		// Relative dates
		{
			name:     "today",
			input:    "today",
			wantDate: time.Now().UTC().Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "yesterday",
			input:    "yesterday",
			wantDate: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "Today with capital T",
			input:    "Today",
			wantDate: time.Now().UTC().Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "Yesterday with capital Y",
			input:    "Yesterday",
			wantDate: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "TODAY all caps",
			input:    "TODAY",
			wantDate: time.Now().UTC().Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "YESTERDAY all caps",
			input:    "YESTERDAY",
			wantDate: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "tomorrow",
			input:    "tomorrow",
			wantDate: time.Now().UTC().AddDate(0, 0, 1).Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "5 days ago",
			input:    "5 days ago",
			wantDate: time.Now().UTC().AddDate(0, 0, -5).Format("2006-01-02"),
			wantErr:  false,
		},
		{
			name:     "1 day ago",
			input:    "1 day ago",
			wantDate: time.Now().UTC().AddDate(0, 0, -1).Format("2006-01-02"),
			wantErr:  false,
		},

		// Edge cases
		{
			name:     "Whitespace trimming",
			input:    "  July 2025  ",
			wantDate: "2025-07-01",
			wantErr:  false,
		},
		{
			name:     "Invalid month name",
			input:    "Jully 2025",
			wantDate: "",
			wantErr:  true,
		},
		{
			name:     "Invalid date format",
			input:    "2025-13-01",
			wantDate: "",
			wantErr:  true,
		},
		{
			name:     "Invalid day for month",
			input:    "31-February-2025",
			wantDate: "",
			wantErr:  true,
		},
		{
			name:     "Empty string",
			input:    "",
			wantDate: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, gotDate, err := parseFlexibleDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseFlexibleDate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && gotDate != tt.wantDate {
				t.Errorf("parseFlexibleDate() gotDate = %v, want %v", gotDate, tt.wantDate)
			}
		})
	}
}

func TestUnitBuildDateFiltersUnit(t *testing.T) {
	tests := []struct {
		name    string
		before  string
		after   string
		on      string
		during  string
		want    map[string]string
		wantErr bool
	}{
		{
			name:    "On with flexible format July 2025",
			before:  "",
			after:   "",
			on:      "July 2025",
			during:  "",
			want:    map[string]string{"on": "2025-07-01"},
			wantErr: false,
		},
		{
			name:    "Before and After with flexible formats",
			before:  "December 2025",
			after:   "January 2025",
			on:      "",
			during:  "",
			want:    map[string]string{"before": "2025-12-01", "after": "2025-01-01"},
			wantErr: false,
		},
		{
			name:    "During with day format",
			before:  "",
			after:   "",
			on:      "",
			during:  "15-July-2025",
			want:    map[string]string{"during": "2025-07-15"},
			wantErr: false,
		},
		{
			name:    "Error: on with other filters",
			before:  "2025-12-01",
			after:   "",
			on:      "July 2025",
			during:  "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Error: during with before",
			before:  "2025-12-01",
			after:   "",
			on:      "",
			during:  "July 2025",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Error: after date is after before date",
			before:  "January 2025",
			after:   "December 2025",
			on:      "",
			during:  "",
			want:    nil,
			wantErr: true,
		},
		{
			name:    "Valid: complex date formats",
			before:  "31-December-2025",
			after:   "1-January-2025",
			on:      "",
			during:  "",
			want:    map[string]string{"before": "2025-12-31", "after": "2025-01-01"},
			wantErr: false,
		},
		{
			name:    "Error: invalid date format",
			before:  "",
			after:   "",
			on:      "Jully 2025",
			during:  "",
			want:    nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildDateFilters(tt.before, tt.after, tt.on, tt.during)
			if (err != nil) != tt.wantErr {
				t.Errorf("buildDateFilters() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if len(got) != len(tt.want) {
					t.Errorf("buildDateFilters() got map length = %v, want %v", len(got), len(tt.want))
					return
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("buildDateFilters() got[%s] = %v, want %v", k, got[k], v)
					}
				}
			}
		})
	}
}

func TestUnitLimitByExpression_Valid(t *testing.T) {
	now := time.Now()

	oneMonthAgo := now.AddDate(0, -1, 0)
	twoMonthsAgo := now.AddDate(0, -2, 0)

	oneMonthSpan := int64(now.Sub(oneMonthAgo).Seconds())
	twoMonthSpan := int64(now.Sub(twoMonthsAgo).Seconds())

	const tolerance = 86400

	tests := []struct {
		name    string
		input   string
		minSecs int64 // inclusive
		maxSecs int64 // exclusive
	}{
		{"1 day", "", 0, 86400}, // default case with no input test
		{"1 day", "1d", 0, 86400},
		{"2 days", "2d", 86400, 172800},
		{"1 week", "1w", 6 * 86400, 7 * 86400},
		{"2 weeks", "2w", 13 * 86400, 14 * 86400},
		{"1 month", "1m", oneMonthSpan - tolerance, oneMonthSpan + tolerance},
		{"2 months", "2m", twoMonthSpan - tolerance, twoMonthSpan + tolerance},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slackLimit, oldestStr, latestStr, err := limitByExpression(tt.input, defaultConversationsExpressionLimit)
			if err != nil {
				t.Fatalf("expected no error for %q, got %v", tt.input, err)
			}
			if slackLimit != 100 {
				t.Errorf("expected slackLimit=100 for %q, got %d", tt.input, slackLimit)
			}

			// Parse the "1234567890.000000" format back to an integer
			o, err := strconv.ParseInt(strings.TrimSuffix(oldestStr, ".000000"), 10, 64)
			if err != nil {
				t.Fatalf("invalid oldest timestamp %q: %v", oldestStr, err)
			}
			l, err := strconv.ParseInt(strings.TrimSuffix(latestStr, ".000000"), 10, 64)
			if err != nil {
				t.Fatalf("invalid latest timestamp %q: %v", latestStr, err)
			}

			if l <= o {
				t.Errorf("for %q expected latest(%d) > oldest(%d)", tt.input, l, o)
			}
			diff := l - o
			if diff < tt.minSecs || diff >= tt.maxSecs {
				t.Errorf(
					"for %q expected span in [%d, %d), got %d",
					tt.input, tt.minSecs, tt.maxSecs, diff,
				)
			}
		})
	}
}

func TestUnitLimitByExpression_Invalid(t *testing.T) {
	invalid := []string{
		"d",   // too short
		"0d",  // zero
		"-1d", // negative
		"1x",  // bad suffix
		"1",   // missing suffix
		"01",  // no suffix + zero value
	}

	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			_, _, _, err := limitByExpression(input, defaultConversationsExpressionLimit)
			if err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

func TestUnitIsChannelAllowedForConfig(t *testing.T) {
	tests := []struct {
		name    string
		channel string
		config  string
		want    bool
	}{
		// Allow all cases
		{"empty config allows all", "C123", "", true},
		{"true allows all", "C123", "true", true},
		{"1 allows all", "C123", "1", true},

		// Allowlist (whitelist) cases
		{"allowlist - channel in list", "C123", "C123,C456", true},
		{"allowlist - second channel in list", "C456", "C123,C456", true},
		{"allowlist - channel NOT in list", "C789", "C123,C456", false},
		{"allowlist - with spaces", "C123", " C123 , C456 ", true},

		// Blocklist cases
		{"blocklist - channel in list", "C123", "!C123,!C456", false},
		{"blocklist - second channel in list", "C456", "!C123,!C456", false},
		{"blocklist - channel NOT in list", "C789", "!C123,!C456", true},
		{"blocklist - with spaces", "C123", " !C123 , !C456 ", false},

		// Single item cases
		{"single allowlist - match", "C123", "C123", true},
		{"single allowlist - no match", "C456", "C123", false},
		{"single blocklist - match", "C123", "!C123", false},
		{"single blocklist - no match", "C456", "!C123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isChannelAllowedForConfig(tt.channel, tt.config)
			if got != tt.want {
				t.Errorf("isChannelAllowedForConfig(%q, %q) = %v, want %v",
					tt.channel, tt.config, got, tt.want)
			}
		})
	}
}

func TestUnitIsSlackUserIDPrefix(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"U prefix", "U0123ABCD", true},
		{"W prefix", "W0123ABCD", true},
		{"orphaned stable ID", "UDELETED123", true},
		{"short stable ID", "U1", true},
		{"uppercase name starting U", "Ursula", false},
		{"uppercase name starting W", "Workspace", false},
		{"lowercase name", "workspace", false},
		{"lowercase ID characters", "U0123abc", false},
		{"ID punctuation", "U0123-ABC", false},
		{"prefix only", "U", false},
		{"plain name not ID", "alice", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSlackUserIDPrefix(tt.s)
			if got != tt.want {
				t.Errorf("isSlackUserIDPrefix(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestUnitNewSearchAuthorFilterAcceptsOrphanedSlackID(t *testing.T) {
	filter := newSearchAuthorFilter("<@UDELETED123>", nil)
	require.NotNil(t, filter)
	assert.Equal(t, "UDELETED123", filter.UserID)
	assert.Empty(t, filter.Name)
}

func TestUnitSearchAuthorFromUsersResponseResolvesDeletedUser(t *testing.T) {
	raw := json.RawMessage(`{
		"ok": true,
		"results": {
			"users": [
				{"user_id": "UDELETED123", "full_name": "Former Engineer"}
			]
		}
	}`)

	filter, err := searchAuthorFromUsersResponse(raw, "Former Engineer")
	require.NoError(t, err)
	require.NotNil(t, filter)
	assert.Equal(t, "UDELETED123", filter.UserID)
}

func TestUnitSearchAuthorFromUsersResponseRejectsSoleFuzzyResult(t *testing.T) {
	raw := json.RawMessage(`{
		"ok": true,
		"results": {
			"users": [
				{"user_id": "UWRONG123", "full_name": "Former Engineering Manager"}
			]
		}
	}`)

	filter, err := searchAuthorFromUsersResponse(raw, "Former Engineer")
	require.NoError(t, err)
	assert.Nil(t, filter)
}

func TestUnitSearchAuthorFromUsersResponseMatchesExactIdentities(t *testing.T) {
	raw := json.RawMessage(`{
		"ok": true,
		"results": {
			"users": [
				{"user_id": "UDELETED123", "full_name": "Former Engineer", "email": "former.engineer@example.com"}
			]
		}
	}`)

	for _, query := range []string{
		"udeleted123",
		"former engineer",
		"FORMER.ENGINEER@EXAMPLE.COM",
		"Former.Engineer",
	} {
		t.Run(query, func(t *testing.T) {
			filter, err := searchAuthorFromUsersResponse(raw, query)
			require.NoError(t, err)
			require.NotNil(t, filter)
			assert.Equal(t, "UDELETED123", filter.UserID)
		})
	}
}

func TestUnitParseParamsToolSearchStoresNormalizedLegacyQuery(t *testing.T) {
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: &fakeAssistantSlack{},
			users: &provider.UsersCache{
				Users:    map[string]slack.User{"UALICE123": {ID: "UALICE123", Name: "alice"}},
				UsersInv: map[string]string{"alice": "UALICE123"},
			},
			channels: &provider.ChannelsCache{
				Channels:    map[string]provider.Channel{"CGENERAL1": {ID: "CGENERAL1", Name: "#general"}},
				ChannelsInv: map[string]string{"#general": "CGENERAL1"},
			},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"search_query":        "launch plan from:alice after:2026-01-01 in:general is:thread",
		"filter_threads_only": true,
	}

	params, err := handler.parseParamsToolSearch(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "launch plan is:thread in:#general from:<@UALICE123> after:2026-01-01", params.query)
	require.NotNil(t, params.authorFilter)
	assert.Equal(t, "UALICE123", params.authorFilter.UserID)
	assert.Equal(t, "CGENERAL1", params.contextChannelID)
	assert.Equal(t, []string{"public_channel"}, params.channelTypes)
}

func TestUnitParseParamsToolSearchRejectsConflictingChannelScopes(t *testing.T) {
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: &fakeAssistantSlack{},
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels: &provider.ChannelsCache{
				Channels: map[string]provider.Channel{
					"CGENERAL1": {ID: "CGENERAL1", Name: "#general"},
					"COTHER001": {ID: "COTHER001", Name: "#other"},
				},
				ChannelsInv: map[string]string{"#general": "CGENERAL1", "#other": "COTHER001"},
			},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"search_query":      "launch in:general",
		"filter_in_channel": "COTHER001",
	}

	params, err := handler.parseParamsToolSearch(context.Background(), request)
	require.Error(t, err)
	assert.Nil(t, params)
	assert.Contains(t, err.Error(), "conflicting channel filters")
}

func TestUnitParseParamsToolSearchResolvesRawStableAuthorModifier(t *testing.T) {
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: &fakeAssistantSlack{},
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels:    &provider.ChannelsCache{Channels: map[string]provider.Channel{}, ChannelsInv: map[string]string{}},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"search_query": "launch from:UDELETED123"}

	params, err := handler.parseParamsToolSearch(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "launch from:<@UDELETED123>", params.query)
	require.NotNil(t, params.authorFilter)
	assert.Equal(t, "UDELETED123", params.authorFilter.UserID)
}

func TestUnitParseParamsToolSearchAllowsFilterOnlyQuery(t *testing.T) {
	handler := &ConversationsHandler{logger: zap.NewNop()}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"filter_threads_only": true,
	}

	params, err := handler.parseParamsToolSearch(context.Background(), request)
	require.NoError(t, err)
	assert.Equal(t, "is:thread", params.query)
}

func TestUnitParseParamsToolSearchRejectsUnverifiableWithFilters(t *testing.T) {
	handler := &ConversationsHandler{logger: zap.NewNop()}

	for _, arguments := range []map[string]any{
		{"search_query": "launch with:UOTHER123"},
		{"search_query": "launch", "filter_users_with": "UOTHER123"},
	} {
		request := mcp.CallToolRequest{}
		request.Params.Arguments = arguments
		params, err := handler.parseParamsToolSearch(context.Background(), request)
		require.Error(t, err)
		assert.Nil(t, params)
		assert.Contains(t, err.Error(), "unsupported")
		assert.Contains(t, err.Error(), "with")
	}
}

func TestUnitParseParamsToolSearchValidatesIntegerLimitBoundaries(t *testing.T) {
	handler := &ConversationsHandler{logger: zap.NewNop()}

	for _, limit := range []int{1, 100} {
		t.Run(fmt.Sprintf("accepts %d", limit), func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"search_query": "launch", "limit": limit}
			params, err := handler.parseParamsToolSearch(context.Background(), request)
			require.NoError(t, err)
			assert.Equal(t, limit, params.limit)
		})
	}

	for _, limit := range []any{0, 101, 1.5} {
		t.Run(fmt.Sprintf("rejects %v", limit), func(t *testing.T) {
			request := mcp.CallToolRequest{}
			request.Params.Arguments = map[string]any{"search_query": "launch", "limit": limit}
			_, err := handler.parseParamsToolSearch(context.Background(), request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "limit")
		})
	}
}

func TestUnitSearchChannelTypesForCachedChannel(t *testing.T) {
	tests := []struct {
		name    string
		channel provider.Channel
		want    []string
	}{
		{
			name:    "MPIM takes precedence over private",
			channel: provider.Channel{IsMpIM: true, IsPrivate: true},
			want:    []string{"mpim"},
		},
		{
			name:    "private channel",
			channel: provider.Channel{IsPrivate: true},
			want:    []string{"private_channel"},
		},
		{
			name:    "public channel",
			channel: provider.Channel{},
			want:    []string{"public_channel"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, searchChannelTypesForCachedChannel(tt.channel))
		})
	}
}

func TestUnitParseSearchRejectsMPIMViaChannelFilter(t *testing.T) {
	fakeProvider := &fakeConversationsProvider{
		slackClient: &fakeAssistantSlack{},
		users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
		channels: &provider.ChannelsCache{
			Channels: map[string]provider.Channel{
				"GMPIM123": {ID: "GMPIM123", Name: "@mpdm-alice-bob-1", IsMpIM: true, IsPrivate: true},
			},
			ChannelsInv: map[string]string{"@mpdm-alice-bob-1": "GMPIM123"},
		},
	}
	handler := &ConversationsHandler{apiProvider: fakeProvider, logger: zap.NewNop()}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"search_query":      "launch",
		"filter_in_channel": "GMPIM123",
	}

	_, err := handler.parseParamsToolSearch(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "filter_in_im_or_mpim")
}

func TestUnitResolveCachedMPIM(t *testing.T) {
	channels := &provider.ChannelsCache{
		Channels: map[string]provider.Channel{
			"GMPIM123": {ID: "GMPIM123", Name: "@mpdm-alice-bob-1", IsMpIM: true, IsPrivate: true},
			"DUSER123": {ID: "DUSER123", Name: "@alice_dm", IsIM: true, IsPrivate: true},
			"GPRIVATE": {ID: "GPRIVATE", Name: "#private", IsPrivate: true},
		},
		ChannelsInv: map[string]string{
			"@mpdm-alice-bob-1": "GMPIM123",
			"@alice_dm":         "DUSER123",
			"#private":          "GPRIVATE",
		},
	}

	for _, input := range []string{"GMPIM123", "@mpdm-alice-bob-1"} {
		t.Run("accepts "+input, func(t *testing.T) {
			channel, err := resolveCachedMPIM(input, channels)
			require.NoError(t, err)
			assert.Equal(t, "GMPIM123", channel.ID)
		})
	}

	for _, input := range []string{"DUSER123", "@alice_dm"} {
		t.Run("rejects one-to-one "+input, func(t *testing.T) {
			_, err := resolveCachedMPIM(input, channels)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "one-to-one DM")
			assert.Contains(t, err.Error(), "im:read")
		})
	}

	_, err := resolveCachedMPIM("GPRIVATE", channels)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not an MPIM")
}

func TestUnitSearchHistoricalAuthorPagesPaginatesToExactMatch(t *testing.T) {
	var cursors []string
	filter, err := searchHistoricalAuthorPages(
		context.Background(),
		"Former Engineer",
		func(_ context.Context, cursor string) (json.RawMessage, error) {
			cursors = append(cursors, cursor)
			if cursor == "" {
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": [{"user_id": "UFUZZY123", "full_name": "Former Engineering Manager"}]},
					"response_metadata": {"next_cursor": "page-two"}
				}`), nil
			}
			return json.RawMessage(`{
				"ok": true,
				"results": {"users": [{"user_id": "UEXACT123", "full_name": "Former Engineer"}]},
				"response_metadata": {"next_cursor": ""}
			}`), nil
		},
	)
	require.NoError(t, err)
	require.NotNil(t, filter)
	assert.Equal(t, "UEXACT123", filter.UserID)
	assert.Equal(t, []string{"", "page-two"}, cursors)
}

func TestUnitSearchHistoricalAuthorPagesFailsClosed(t *testing.T) {
	t.Run("API errors propagate", func(t *testing.T) {
		sentinel := errors.New("network down")
		_, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(context.Context, string) (json.RawMessage, error) {
				return nil, sentinel
			},
		)
		require.ErrorIs(t, err, sentinel)
	})

	t.Run("no exact result is user not found", func(t *testing.T) {
		_, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(context.Context, string) (json.RawMessage, error) {
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": [{"user_id": "UFUZZY123", "full_name": "Former Engineering Manager"}]},
					"response_metadata": {"next_cursor": ""}
				}`), nil
			},
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `user "Former Engineer" not found`)
	})

	t.Run("pagination has a ten page safety bound", func(t *testing.T) {
		calls := 0
		_, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(context.Context, string) (json.RawMessage, error) {
				calls++
				return json.RawMessage(fmt.Sprintf(`{
					"ok": true,
					"results": {"users": []},
					"response_metadata": {"next_cursor": "page-%d"}
				}`, calls+1)), nil
			},
		)
		require.Error(t, err)
		assert.Equal(t, 10, calls)
		assert.Contains(t, err.Error(), "pagination truncated")
	})

	t.Run("repeated cursor is explicit", func(t *testing.T) {
		calls := 0
		_, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(context.Context, string) (json.RawMessage, error) {
				calls++
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": []},
					"response_metadata": {"next_cursor": "same-page"}
				}`), nil
			},
		)
		require.Error(t, err)
		assert.Equal(t, 2, calls)
		assert.Contains(t, err.Error(), "repeated cursor")
	})
}

func TestUnitSearchHistoricalAuthorPagesRejectsAmbiguousAndInvalidMatches(t *testing.T) {
	t.Run("exhausts pages before reporting ambiguity", func(t *testing.T) {
		filter, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(_ context.Context, cursor string) (json.RawMessage, error) {
				if cursor == "" {
					return json.RawMessage(`{
						"ok": true,
						"results": {"users": [{"user_id": "UONE12345", "full_name": "Former Engineer"}]},
						"response_metadata": {"next_cursor": "page-two"}
					}`), nil
				}
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": [{"user_id": "UTWO45678", "full_name": "former engineer"}]},
					"response_metadata": {"next_cursor": ""}
				}`), nil
			},
		)
		assert.Nil(t, filter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ambiguous")
	})

	t.Run("matching candidate requires a stable user ID", func(t *testing.T) {
		_, err := searchHistoricalAuthorPages(
			context.Background(),
			"Former Engineer",
			func(context.Context, string) (json.RawMessage, error) {
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": [{"user_id": "", "full_name": "Former Engineer"}]},
					"response_metadata": {"next_cursor": ""}
				}`), nil
			},
		)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "stable user ID")
	})

	t.Run("stable ID outranks lower-priority name match", func(t *testing.T) {
		filter, err := searchHistoricalAuthorPages(
			context.Background(),
			"UWINNER123",
			func(context.Context, string) (json.RawMessage, error) {
				return json.RawMessage(`{
					"ok": true,
					"results": {"users": [
						{"user_id": "ULOWER456", "full_name": "UWINNER123"},
						{"user_id": "UWINNER123", "full_name": "Someone Else"}
					]},
					"response_metadata": {"next_cursor": ""}
				}`), nil
			},
		)
		require.NoError(t, err)
		require.NotNil(t, filter)
		assert.Equal(t, "UWINNER123", filter.UserID)
	})
}

type fakeAssistantSlack struct {
	provider.SlackAPI
	pages    map[string]json.RawMessage
	requests []provider.AssistantSearchContextRequest
}

func (f *fakeAssistantSlack) AssistantSearchContext(_ context.Context, request provider.AssistantSearchContextRequest) (json.RawMessage, error) {
	f.requests = append(f.requests, request)
	page, ok := f.pages[request.Cursor]
	if !ok {
		return nil, fmt.Errorf("unexpected cursor %q", request.Cursor)
	}
	return page, nil
}

type boundedAssistantSlack struct {
	provider.SlackAPI
	requests []provider.AssistantSearchContextRequest
}

func (f *boundedAssistantSlack) AssistantSearchContext(_ context.Context, request provider.AssistantSearchContextRequest) (json.RawMessage, error) {
	f.requests = append(f.requests, request)
	nextCursor := ""
	if len(f.requests) <= 10 {
		nextCursor = fmt.Sprintf("unique-page-%d", len(f.requests)+1)
	}
	return json.RawMessage(fmt.Sprintf(`{
		"ok": true,
		"results": {"messages": []},
		"response_metadata": {"next_cursor": %q}
	}`, nextCursor)), nil
}

type fakeConversationsProvider struct {
	slackClient provider.SlackAPI
	users       *provider.UsersCache
	channels    *provider.ChannelsCache
}

func (f *fakeConversationsProvider) ServerTransport() string  { return "stdio" }
func (f *fakeConversationsProvider) IsReady() (bool, error)   { return true, nil }
func (f *fakeConversationsProvider) Slack() provider.SlackAPI { return f.slackClient }
func (f *fakeConversationsProvider) ProvideUsersMap() *provider.UsersCache {
	return f.users
}
func (f *fakeConversationsProvider) ProvideChannelsMaps() *provider.ChannelsCache {
	return f.channels
}
func (f *fakeConversationsProvider) SearchUsers(context.Context, string, int) ([]slack.User, error) {
	return nil, nil
}
func (f *fakeConversationsProvider) IsBotToken() bool { return false }
func (f *fakeConversationsProvider) IsOAuth() bool    { return true }
func (f *fakeConversationsProvider) ForceRefreshChannels(context.Context) error {
	return nil
}
func (f *fakeConversationsProvider) PatchUser(context.Context, string) (*slack.User, error) {
	return nil, errors.New("not found")
}

func TestUnitConversationsSearchHandlerAggregatesNormalizedCSV(t *testing.T) {
	api := &fakeAssistantSlack{pages: map[string]json.RawMessage{
		"start": json.RawMessage(`{
			"ok": true,
			"results": {"messages": [
				{"author_email":"other@example.com","author_name":"Other Person","author_user_id":"UOTHER123","channel_id":"CSEARCH123","channel_name":"search","content":"ignore me","message_ts":"1760000000.000001","permalink":"https://example.slack.com/archives/CSEARCH123/p1760000000000001","team_id":"T123","secret":"must-not-leak"},
				{"author_email":"former@example.com","author_name":"Former Engineer","author_user_id":"UAUTHOR123","channel_id":"COTHER123","channel_name":"other","content":"wrong channel","message_ts":"1760000000.500001","permalink":"https://example.slack.com/archives/COTHER123/p1760000000500001","team_id":"T123","secret":"must-not-leak"},
				{"author_email":"former@example.com","author_name":"Former Engineer","author_user_id":"UAUTHOR123","channel_id":"CSEARCH123","channel_name":"search","content":"first <https://example.com|docs>","message_ts":"1760000001.000002","permalink":"https://example.slack.com/archives/CSEARCH123/p1760000001000002?thread_ts=1759999999.000001","team_id":"T123","secret":"must-not-leak"},
				{"author_email":"former@example.com","author_name":"Former Engineer","author_user_id":"UAUTHOR123","channel_id":"CSEARCH123","channel_name":"search","content":"second","is_author_bot":true,"message_ts":"1760000002.000003","permalink":"https://example.slack.com/archives/CSEARCH123/p1760000002000003","team_id":"T123","secret":"must-not-leak"}
			]},
			"response_metadata": {"next_cursor": "page-two"}
		}`),
		"page-two": json.RawMessage(`{
			"ok": true,
			"results": {"messages": [
				{"author_email":"former@example.com","author_name":"Former Engineer","author_user_id":"UAUTHOR123","channel_id":"CSEARCH123","channel_name":"search","content":"third","message_ts":"1760000003.000004","permalink":"https://example.slack.com/archives/CSEARCH123/p1760000003000004","team_id":"T123","secret":"must-not-leak"}
			]},
			"response_metadata": {"next_cursor": "page-three"}
		}`),
	}}
	fakeProvider := &fakeConversationsProvider{
		slackClient: api,
		users: &provider.UsersCache{
			Users: map[string]slack.User{
				"UAUTHOR123": {ID: "UAUTHOR123", Name: "former.engineer"},
			},
			UsersInv: map[string]string{"former.engineer": "UAUTHOR123"},
		},
		channels: &provider.ChannelsCache{
			Channels: map[string]provider.Channel{
				"CSEARCH123": {ID: "CSEARCH123", Name: "#search", IsPrivate: true},
			},
			ChannelsInv: map[string]string{"#search": "CSEARCH123"},
		},
	}
	handler := &ConversationsHandler{apiProvider: fakeProvider, logger: zap.NewNop()}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"search_query":        "launch plan after:2026-01-01",
		"filter_threads_only": true,
		"filter_in_channel":   "CSEARCH123",
		"filter_users_from":   "UAUTHOR123",
		"cursor":              "start",
		"limit":               3,
	}

	result, err := handler.ConversationsSearchHandler(context.Background(), request)
	require.NoError(t, err)
	require.Len(t, api.requests, 2)
	assert.Equal(t, "launch plan is:thread in:#search from:<@UAUTHOR123> after:2026-01-01", api.requests[0].Query)
	assert.Equal(t, []string{"private_channel"}, api.requests[0].ChannelTypes)
	assert.Equal(t, []string{"messages"}, api.requests[0].ContentTypes)
	assert.Equal(t, "CSEARCH123", api.requests[0].ContextChannelID)
	assert.Equal(t, "start", api.requests[0].Cursor)
	assert.Equal(t, 3, api.requests[0].Limit)
	assert.True(t, api.requests[0].IncludeContextMessages)
	assert.True(t, api.requests[0].DisableSemanticSearch)
	assert.Equal(t, "page-two", api.requests[1].Cursor)
	assert.Equal(t, 1, api.requests[1].Limit)

	require.Len(t, result.Content, 1)
	content, ok := result.Content[0].(mcp.TextContent)
	require.True(t, ok)
	assert.NotContains(t, content.Text, "must-not-leak")
	assert.NotContains(t, content.Text, "author_email")

	rows, err := csv.NewReader(strings.NewReader(content.Text)).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 4)
	columns := make(map[string]int, len(rows[0]))
	for i, name := range rows[0] {
		columns[name] = i
	}
	for _, requiredColumn := range []string{"MsgID", "UserID", "UserName", "RealName", "Channel", "ThreadTs", "Text", "Time", "Permalink", "BotName", "Cursor"} {
		_, exists := columns[requiredColumn]
		assert.Truef(t, exists, "missing normalized Message column %s", requiredColumn)
	}
	assert.Equal(t, "1760000001.000002", rows[1][columns["MsgID"]])
	assert.Equal(t, "UAUTHOR123", rows[1][columns["UserID"]])
	assert.Equal(t, "former.engineer", rows[1][columns["UserName"]])
	assert.Equal(t, "Former Engineer", rows[1][columns["RealName"]])
	assert.Equal(t, "CSEARCH123 (#search)", rows[1][columns["Channel"]])
	assert.Equal(t, "1759999999.000001", rows[1][columns["ThreadTs"]])
	assert.Equal(t, text.ProcessText("first <https://example.com|docs>"), rows[1][columns["Text"]])
	wantTime, err := text.TimestampToIsoRFC3339("1760000001.000002")
	require.NoError(t, err)
	assert.Equal(t, wantTime, rows[1][columns["Time"]])
	assert.Empty(t, rows[1][columns["Cursor"]])
	assert.Equal(t, "Former Engineer", rows[2][columns["BotName"]])
	assert.Empty(t, rows[2][columns["Cursor"]])
	assert.Equal(t, "page-three", rows[3][columns["Cursor"]])
}

func TestUnitConversationsSearchHandlerRejectsRepeatedCursor(t *testing.T) {
	api := &fakeAssistantSlack{pages: map[string]json.RawMessage{
		"start": json.RawMessage(`{
			"ok": true,
			"results": {"messages": []},
			"response_metadata": {"next_cursor": "start"}
		}`),
	}}
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: api,
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels:    &provider.ChannelsCache{Channels: map[string]provider.Channel{}, ChannelsInv: map[string]string{}},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"search_query": "launch", "cursor": "start", "limit": 1}

	_, err := handler.ConversationsSearchHandler(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated cursor")
}

func TestUnitConversationsSearchHandlerRejectsRepeatedCursorEvenWhenPageFillsLimit(t *testing.T) {
	api := &fakeAssistantSlack{pages: map[string]json.RawMessage{
		"start": json.RawMessage(`{
			"ok": true,
			"results": {"messages": [
				{"author_name":"Engineer","author_user_id":"UAUTHOR123","channel_id":"CSEARCH123","channel_name":"search","content":"match","message_ts":"1760000001.000002","permalink":"https://example.slack.com/archives/CSEARCH123/p1760000001000002"}
			]},
			"response_metadata": {"next_cursor": "start"}
		}`),
	}}
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: api,
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels:    &provider.ChannelsCache{Channels: map[string]provider.Channel{}, ChannelsInv: map[string]string{}},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{"search_query": "launch", "cursor": "start", "limit": 1}

	_, err := handler.ConversationsSearchHandler(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "repeated cursor")
}

func TestUnitDecodeAssistantSearchMessagePageRejectsMalformedSuccess(t *testing.T) {
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"ok":true}`),
		json.RawMessage(`{"ok":true,"results":{}}`),
		json.RawMessage(`{"ok":true,"results":{"files":[]}}`),
		json.RawMessage(`{"ok":true,"results":{"messages":null}}`),
	} {
		page, err := decodeAssistantSearchMessagePage(raw)
		require.Error(t, err)
		assert.Empty(t, page.Messages)
		assert.Contains(t, err.Error(), "messages")
	}
}

func TestUnitConversationsSearchHandlerFailsExplicitlyAtPageBound(t *testing.T) {
	api := &boundedAssistantSlack{}
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: api,
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels:    &provider.ChannelsCache{Channels: map[string]provider.Channel{}, ChannelsInv: map[string]string{}},
		},
		logger: zap.NewNop(),
	}
	request := mcp.CallToolRequest{}
	request.Params.Arguments = map[string]any{
		"search_query": "launch from:UDELETED123",
		"limit":        1,
	}

	_, err := handler.ConversationsSearchHandler(context.Background(), request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pagination truncated")
	assert.Len(t, api.requests, 10)
}

func TestUnitConvertAssistantSearchUsesAuthorNameWhenUserLookupFails(t *testing.T) {
	handler := &ConversationsHandler{
		apiProvider: &fakeConversationsProvider{
			slackClient: &fakeAssistantSlack{},
			users:       &provider.UsersCache{Users: map[string]slack.User{}, UsersInv: map[string]string{}},
			channels:    &provider.ChannelsCache{Channels: map[string]provider.Channel{}, ChannelsInv: map[string]string{}},
		},
		logger: zap.NewNop(),
	}
	messages := handler.convertMessagesFromAssistantSearch(context.Background(), []assistantSearchMessage{{
		AuthorName: "Former Engineer", AuthorUserID: "UDELETED123", ChannelID: "C1",
		Content: "historical", MessageTS: "1760000001.000002",
	}})

	require.Len(t, messages, 1)
	assert.Equal(t, "UDELETED123", messages[0].UserName)
	assert.Equal(t, "Former Engineer", messages[0].RealName)
}

func TestUnitValidateAssistantSearchToken(t *testing.T) {
	require.NoError(t, validateAssistantSearchToken(true, false))

	err := validateAssistantSearchToken(true, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "user token")
	assert.Contains(t, err.Error(), "action_token")

	err = validateAssistantSearchToken(false, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "User OAuth token")
	assert.Contains(t, err.Error(), "browser session tokens")
}
