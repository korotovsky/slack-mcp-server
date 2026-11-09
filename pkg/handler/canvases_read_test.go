package handler

import (
	"context"
	"fmt"
	"github.com/korotovsky/slack-mcp-server/pkg/provider"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
	"io"
	"strings"
	"testing"
)

type canvasReadMock struct {
	provider.SlackAPI
	file                                *slack.File
	content                             string
	metadataErr, downloadErr, lookupErr error
	downloads, lookups                  int
}

func (m *canvasReadMock) GetFileInfoContext(context.Context, string, int, int) (*slack.File, []slack.Comment, *slack.Paging, error) {
	return m.file, nil, nil, m.metadataErr
}
func (m *canvasReadMock) GetFileContext(_ context.Context, _ string, w io.Writer) error {
	m.downloads++
	if m.downloadErr != nil {
		return m.downloadErr
	}
	_, err := io.WriteString(w, m.content)
	return err
}
func (m *canvasReadMock) LookupCanvasSectionsContext(context.Context, slack.LookupCanvasSectionsParams) ([]slack.CanvasSection, error) {
	m.lookups++
	return []slack.CanvasSection{{ID: "temp:filtered"}}, m.lookupErr
}

func TestUnitCanvasRead(t *testing.T) {
	for _, scenario := range []string{"success", "filtered", "no_sections", "invalid_id", "not_canvas", "wrong_id", "nil_file", "unsafe_url", "missing_url", "metadata_error", "download_error", "lookup_error", "oversize", "inconsistent_filter"} {
		t.Run(scenario, func(t *testing.T) {
			mock := &canvasReadMock{file: &slack.File{ID: "F123ABC", Filetype: "quip", Title: "Title", URLPrivateDownload: "https://files.slack.com/files-pri/example/download"}, content: `<h1 id='temp:A'>Title</h1><p id="temp:B">Body</p>`}
			args := map[string]any{"canvas_id": "F123ABC"}
			switch scenario {
			case "filtered":
				args["contains_text"] = "Title"
			case "no_sections":
				args["include_sections"] = false
			case "invalid_id":
				args["canvas_id"] = "C123ABC"
			case "not_canvas":
				mock.file.Filetype = "pdf"
			case "wrong_id":
				mock.file.ID = "F456DEF"
			case "nil_file":
				mock.file = nil
			case "unsafe_url":
				mock.file.URLPrivateDownload = "https://example.com/file"
			case "missing_url":
				mock.file.URLPrivateDownload = ""
			case "metadata_error":
				mock.metadataErr = fmt.Errorf("missing_scope")
			case "download_error":
				mock.downloadErr = fmt.Errorf("private-url-sensitive")
			case "lookup_error":
				args["contains_text"] = "Title"
				mock.lookupErr = fmt.Errorf("missing_scope")
			case "oversize":
				mock.content = strings.Repeat("x", maxCanvasReadBytes+1)
			case "inconsistent_filter":
				args["include_sections"] = false
				args["contains_text"] = "Title"
			}
			r := mcp.CallToolRequest{}
			r.Params.Arguments = args
			result, err := readCanvas(context.Background(), mock, r)
			if scenario != "success" && scenario != "filtered" && scenario != "no_sections" {
				require.Error(t, err)
				require.Nil(t, result)
				require.NotContains(t, err.Error(), "private-url-sensitive")
				return
			}
			require.NoError(t, err)
			require.Equal(t, mock.content, result.Content)
			require.Equal(t, "text/html", result.ContentType)
			if scenario == "filtered" {
				require.Equal(t, 1, mock.lookups)
				require.Equal(t, "temp:filtered", result.Sections[0].ID)
			} else if scenario == "no_sections" {
				require.Empty(t, result.Sections)
			} else {
				require.Len(t, result.Sections, 2)
				require.Zero(t, mock.lookups)
			}
		})
	}
}

func TestUnitCanvasSectionHTMLParsing(t *testing.T) {
	content := `<p data-id="temp:not-an-id" id='temp:A'>A</p><p id="temp:A">duplicate</p><p id="temp:B&amp;C">B</p><!-- id="temp:comment" --><script>var x='id="temp:script"';</script>`
	require.Equal(t, []slack.CanvasSection{{ID: "temp:A"}, {ID: "temp:B&C"}}, parseCanvasSectionIDs(content))
	require.Empty(t, parseCanvasSectionIDs("no sections"))
}

func TestUnitCanvasDownloadURLs(t *testing.T) {
	for _, u := range []string{"https://files.slack.com/file", "https://files.slack-gov.com/file"} {
		require.True(t, validCanvasDownloadURL(u))
	}
	for _, u := range []string{"http://files.slack.com/file", "https://files.slack.com.evil.test/file", "https://user:pass@files.slack.com/file", "https://files.slack.com:123/file", "file:///tmp/foo", ""} {
		require.False(t, validCanvasDownloadURL(u))
	}
}
