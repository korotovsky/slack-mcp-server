package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/slack-go/slack"
	"github.com/stretchr/testify/require"
)

func TestCanvasAPI(t *testing.T) {
	for _, apiError := range []string{"", "missing_scope", "access_denied", "canvas_editing_failed", "canvas_editing_locked"} {
		t.Run("edit_"+apiError, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, "/canvases.edit", r.URL.Path)
				require.NoError(t, r.ParseForm())
				require.Equal(t, "F123ABC", r.Form.Get("canvas_id"))
				var changes []slack.CanvasChange
				require.NoError(t, json.Unmarshal([]byte(r.Form.Get("changes")), &changes))
				require.Len(t, changes, 1)
				require.Equal(t, "insert_at_end", changes[0].Operation)
				require.Equal(t, "hello", changes[0].DocumentContent.Markdown)
				w.Header().Set("Content-Type", "application/json")
				if apiError != "" {
					fmt.Fprintf(w, `{"ok":false,"error":%q}`, apiError)
				} else {
					fmt.Fprint(w, `{"ok":true}`)
				}
			}))
			defer srv.Close()
			client := &MCPSlackClient{slackClient: slack.New("test-token", slack.OptionAPIURL(srv.URL+"/"))}
			err := client.EditCanvasContext(context.Background(), slack.EditCanvasParams{CanvasID: "F123ABC", Changes: []slack.CanvasChange{{Operation: "insert_at_end", DocumentContent: slack.DocumentContent{Type: "markdown", Markdown: "hello"}}}})
			if apiError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, apiError)
			}
			require.Equal(t, 1, calls, "mutations must not be retried")
		})
	}
	t.Run("lookup", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/canvases.sections.lookup", r.URL.Path)
			require.NoError(t, r.ParseForm())
			require.JSONEq(t, `{"contains_text":"Project"}`, r.Form.Get("criteria"))
			fmt.Fprint(w, `{"ok":true,"sections":[{"id":"temp:section"}]}`)
		}))
		defer srv.Close()
		client := &MCPSlackClient{slackClient: slack.New("test-token", slack.OptionAPIURL(srv.URL+"/"))}
		sections, err := client.LookupCanvasSectionsContext(context.Background(), slack.LookupCanvasSectionsParams{CanvasID: "F123ABC", Criteria: slack.LookupCanvasSectionsCriteria{ContainsText: "Project"}})
		require.NoError(t, err)
		require.Equal(t, []slack.CanvasSection{{ID: "temp:section"}}, sections)
	})
}
