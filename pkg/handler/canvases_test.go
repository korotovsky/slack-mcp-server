package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseSectionIDs(t *testing.T) {
	t.Run("extracts ids in document order", func(t *testing.T) {
		content := `<div class="quip-canvas-content">` +
			`<h1 id="temp:C:AAA1">Title</h1>` +
			`<p id="temp:C:BBB2" class="line">Body</p>` +
			`<h2 id="temp:C:CCC3">Section</h2>` +
			`</div>`

		sections := parseSectionIDs(content)

		assert.Equal(t, []CanvasSection{
			{ID: "temp:C:AAA1"},
			{ID: "temp:C:BBB2"},
			{ID: "temp:C:CCC3"},
		}, sections)
	})

	t.Run("deduplicates repeated ids", func(t *testing.T) {
		content := `<h1 id="temp:C:AAA1">A</h1><p id="temp:C:AAA1">A again</p>`

		sections := parseSectionIDs(content)

		assert.Equal(t, []CanvasSection{{ID: "temp:C:AAA1"}}, sections)
	})

	t.Run("ignores non-section ids", func(t *testing.T) {
		content := `<div id="not-a-section"><h1 id="temp:C:AAA1">A</h1></div>`

		sections := parseSectionIDs(content)

		assert.Equal(t, []CanvasSection{{ID: "temp:C:AAA1"}}, sections)
	})

	t.Run("returns nil when there are no sections", func(t *testing.T) {
		assert.Nil(t, parseSectionIDs("<div>no ids here</div>"))
	})
}

func TestRequiresSection(t *testing.T) {
	needs := []string{"insert_before", "insert_after", "replace", "delete"}
	for _, op := range needs {
		assert.True(t, requiresSection(op), "%s should require a section_id", op)
	}

	for _, op := range []string{"insert_at_start", "insert_at_end"} {
		assert.False(t, requiresSection(op), "%s should not require a section_id", op)
	}
}
