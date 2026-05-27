package handler

import (
	"context"
	"encoding/base64"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/korotovsky/slack-mcp-server/pkg/test/util"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
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
				Model: "gpt-4.1-mini",
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

func TestUnitResolveAllowedFilePath(t *testing.T) {
	tmp := t.TempDir()
	allowed := filepath.Join(tmp, "allowed")
	outside := filepath.Join(tmp, "outside")
	require.NoError(t, os.MkdirAll(allowed, 0o755))
	require.NoError(t, os.MkdirAll(outside, 0o755))

	goodFile := filepath.Join(allowed, "ok.txt")
	require.NoError(t, os.WriteFile(goodFile, []byte("hello"), 0o644))

	outsideFile := filepath.Join(outside, "bad.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("nope"), 0o644))

	escape := filepath.Join(allowed, "escape.txt")
	require.NoError(t, os.Symlink(outsideFile, escape))

	bigFile := filepath.Join(allowed, "big.bin")
	big := make([]byte, maxFileSizeBytes+1)
	require.NoError(t, os.WriteFile(bigFile, big, 0o644))

	t.Run("empty allowlist rejects all paths", func(t *testing.T) {
		_, _, err := resolveAllowedFilePath(goodFile, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILES_UPLOAD_PATHS")
	})

	t.Run("path inside allowlist is accepted", func(t *testing.T) {
		resolved, size, err := resolveAllowedFilePath(goodFile, allowed)
		require.NoError(t, err)
		// resolveAllowedFilePath returns the EvalSymlinks form; compare against
		// that, since TempDir is reached through a symlink on some platforms
		// (see https://github.com/golang/go/issues/56259).
		wantResolved, evalErr := filepath.EvalSymlinks(goodFile)
		require.NoError(t, evalErr)
		assert.Equal(t, wantResolved, resolved)
		assert.Equal(t, 5, size)
	})

	t.Run("path outside allowlist is rejected", func(t *testing.T) {
		_, _, err := resolveAllowedFilePath(outsideFile, allowed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not inside any directory")
	})

	t.Run("symlink escaping allowlist is rejected", func(t *testing.T) {
		_, _, err := resolveAllowedFilePath(escape, allowed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not inside any directory")
	})

	t.Run("file exceeding size cap is rejected", func(t *testing.T) {
		_, _, err := resolveAllowedFilePath(bigFile, allowed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum allowed size")
	})

	t.Run("directory path is rejected", func(t *testing.T) {
		_, _, err := resolveAllowedFilePath(allowed, allowed)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "is a directory")
	})

	t.Run("multi-entry allowlist accepts second entry", func(t *testing.T) {
		other := filepath.Join(tmp, "other")
		require.NoError(t, os.MkdirAll(other, 0o755))
		resolved, _, err := resolveAllowedFilePath(goodFile, other+", "+allowed)
		require.NoError(t, err)
		assert.Equal(t, goodFile, resolved)
	})

	t.Run("relative path resolves against cwd", func(t *testing.T) {
		// chdir into the allowed dir and pass a bare filename so the
		// filepath.Abs (relative-to-cwd) branch is actually exercised.
		t.Chdir(allowed)
		_, size, err := resolveAllowedFilePath("ok.txt", allowed)
		require.NoError(t, err)
		assert.Equal(t, 5, size)
	})
}

func TestUnitParseFilesUploadParams(t *testing.T) {
	ch := &ConversationsHandler{logger: zap.NewNop()}

	makeReq := func(args map[string]any) mcp.CallToolRequest {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = args
		return req
	}

	t.Run("disabled when env var unset", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_UPLOAD_TOOL", "")
		t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename": "a.txt", "content": "hi",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "disabled")
	})

	t.Run("enabled via SLACK_MCP_ENABLED_TOOLS", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_UPLOAD_TOOL", "")
		t.Setenv("SLACK_MCP_ENABLED_TOOLS", "files_upload")
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename": "a.txt", "content": "hi",
		}))
		require.NoError(t, err)
	})

	// All remaining cases run with the tool enabled.
	t.Setenv("SLACK_MCP_FILES_UPLOAD_TOOL", "true")
	t.Setenv("SLACK_MCP_ENABLED_TOOLS", "")

	t.Run("missing filename is rejected", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"content": "hi",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "filename")
	})

	t.Run("no content source is rejected", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename": "a.txt",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exactly one")
	})

	t.Run("two content sources are mutually exclusive", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.txt",
			"content":        "hi",
			"content_base64": base64.StdEncoding.EncodeToString([]byte("hi")),
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "mutually exclusive")
	})

	t.Run("text content sets content and size", func(t *testing.T) {
		p, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":     "a.txt",
			"content":      "hello",
			"snippet_type": "python",
		}))
		require.NoError(t, err)
		assert.Equal(t, "hello", p.content)
		assert.Equal(t, 5, p.fileSize)
		assert.Equal(t, "python", p.snippetType)
		assert.Empty(t, p.contentBytes)
	})

	t.Run("text content exceeding size cap is rejected", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename": "a.txt",
			"content":  strings.Repeat("a", maxFileSizeBytes+1),
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})

	t.Run("invalid base64 is rejected with a clear message", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.bin",
			"content_base64": "not valid base64!",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not valid base64")
	})

	t.Run("base64 decoded over cap is rejected", func(t *testing.T) {
		payload := base64.StdEncoding.EncodeToString(make([]byte, maxFileSizeBytes+1))
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.bin",
			"content_base64": payload,
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})

	t.Run("base64 content populates bytes and size", func(t *testing.T) {
		p, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.bin",
			"content_base64": base64.StdEncoding.EncodeToString([]byte{0x01, 0x02, 0x03}),
		}))
		require.NoError(t, err)
		assert.Equal(t, []byte{0x01, 0x02, 0x03}, p.contentBytes)
		assert.Equal(t, 3, p.fileSize)
	})

	t.Run("whitespace-only base64 is rejected as zero bytes", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.bin",
			"content_base64": "   \n\t ",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "zero bytes")
	})

	t.Run("line-wrapped base64 with whitespace decodes cleanly", func(t *testing.T) {
		// Emulate `base64` CLI output: 76-col line wrap plus stray surrounding
		// whitespace from the LLM concatenating fragments.
		payload := make([]byte, 200)
		for i := range payload {
			payload[i] = byte(i)
		}
		encoded := base64.StdEncoding.EncodeToString(payload)
		var wrapped strings.Builder
		for i := 0; i < len(encoded); i += 76 {
			end := i + 76
			if end > len(encoded) {
				end = len(encoded)
			}
			wrapped.WriteString(encoded[i:end])
			wrapped.WriteString("\n")
		}
		p, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":       "a.bin",
			"content_base64": "  \n" + wrapped.String() + "\n  ",
		}))
		require.NoError(t, err)
		assert.Equal(t, payload, p.contentBytes)
		assert.Equal(t, len(payload), p.fileSize)
	})

	t.Run("file_path without allowlist env var is rejected", func(t *testing.T) {
		t.Setenv("SLACK_MCP_FILES_UPLOAD_PATHS", "")
		tmp := t.TempDir()
		path := filepath.Join(tmp, "x.txt")
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":  "x.txt",
			"file_path": path,
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SLACK_MCP_FILES_UPLOAD_PATHS")
	})

	t.Run("file_path inside allowlist is accepted", func(t *testing.T) {
		tmp := t.TempDir()
		path := filepath.Join(tmp, "x.txt")
		require.NoError(t, os.WriteFile(path, []byte("xyz"), 0o644))
		t.Setenv("SLACK_MCP_FILES_UPLOAD_PATHS", tmp)
		p, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":  "x.txt",
			"file_path": path,
		}))
		require.NoError(t, err)
		assert.Equal(t, 3, p.fileSize)
		assert.Equal(t, []byte("xyz"), p.contentBytes)
	})

	t.Run("dotted non-numeric thread_ts is rejected", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":  "a.txt",
			"content":   "hi",
			"thread_ts": "abc.def",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread_ts")
	})

	t.Run("invalid thread_ts format is rejected", func(t *testing.T) {
		_, err := ch.parseParamsToolFilesUpload(context.Background(), makeReq(map[string]any{
			"filename":  "a.txt",
			"content":   "hi",
			"thread_ts": "not-a-ts",
		}))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "thread_ts")
	})
}
