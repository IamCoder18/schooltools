package content_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aarav/schooltools/internal/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tocFixture() content.TocResponse {
	url1 := "/content/enforced/x/outline.pdf"
	url2 := "/content/enforced/x/safety.pdf"
	url3 := "https://example.com"
	return content.TocResponse{
		Modules: []content.TocModule{
			{
				ModuleId:         1,
				Title:            "Course Start-Up",
				LastModifiedDate: "2026-08-31T00:00:00.000Z",
				Modules: []content.TocModule{
					{
						ModuleId:         2,
						Title:            "Sub",
						LastModifiedDate: "2026-08-30T00:00:00.000Z",
						Topics: []content.TocTopic{
							{
								TopicId:          100,
								Title:            "Outline",
								TypeIdentifier:   "File",
								Url:              &url1,
								LastModifiedDate: "2026-08-31T12:00:00.000Z",
								ActivityId:       "https://ids.brightspace.com/activities/contenttopic/X",
								TopicType:        1,
							},
						},
					},
				},
				Topics: []content.TocTopic{
					{
						TopicId:          50,
						Title:            "Safety Contract",
						TypeIdentifier:   "File",
						Url:              &url2,
						LastModifiedDate: "2026-08-31T13:00:00.000Z",
						ActivityId:       "https://ids.brightspace.com/activities/contenttopic/Y",
						TopicType:        1,
					},
					{
						TopicId:          51,
						Title:            "Science in the news",
						TypeIdentifier:   "Link",
						Url:              &url3,
						LastModifiedDate: "2026-08-31T14:00:00.000Z",
						ActivityId:       "https://ids.brightspace.com/activities/contenttopic/Z",
						TopicType:        2,
					},
				},
			},
		},
	}
}

func TestFlattenTocEmpty(t *testing.T) {
	rows := content.FlattenToc(nil)
	assert.Empty(t, rows)
}

func TestFlattenTocNested(t *testing.T) {
	toc := tocFixture()
	rows := content.FlattenToc(toc.Modules)
	assert.Len(t, rows, 5)

	got := make([]map[string]any, len(rows))
	for i, r := range rows {
		got[i] = map[string]any{
			"kind":  r.Kind,
			"depth": r.Depth,
			"id":    r.Id,
			"title": r.Title,
			"type":  r.Type,
		}
	}
	expected := []map[string]any{
		{"kind": "MODULE", "depth": 0, "id": 1, "title": "Course Start-Up", "type": "Module"},
		{"kind": "TOPIC", "depth": 1, "id": 50, "title": "Safety Contract", "type": "File"},
		{"kind": "TOPIC", "depth": 1, "id": 51, "title": "Science in the news", "type": "Link"},
		{"kind": "MODULE", "depth": 1, "id": 2, "title": "Sub", "type": "Module"},
		{"kind": "TOPIC", "depth": 2, "id": 100, "title": "Outline", "type": "File"},
	}
	assert.Equal(t, expected, got)
}

func TestFlattenTocPreservesLastModified(t *testing.T) {
	toc := tocFixture()
	rows := content.FlattenToc(toc.Modules)
	var outline *content.FlatRow
	for i := range rows {
		if rows[i].Id == 100 {
			outline = &rows[i]
			break
		}
	}
	require.NotNil(t, outline)
	assert.Equal(t, "2026-08-31T12:00:00.000Z", outline.LastModified)
}

func TestFlattenTocTypeFallback(t *testing.T) {
	rows := content.FlattenToc([]content.TocModule{
		{
			ModuleId: 9, Title: "Bare", LastModifiedDate: "2026-01-01T00:00:00.000Z",
			Topics: []content.TocTopic{
				{
					TopicId: 999, Title: "No type", Url: nil,
					LastModifiedDate: "2026-01-01T00:00:00.000Z",
					ActivityId:       "a", TopicType: 2,
				},
			},
		},
	})
	require.Len(t, rows, 2)
	assert.Equal(t, "Link", rows[1].Type)
	assert.Equal(t, "", rows[1].URL)
}

func TestFlatRowJSONHasExpectedFields(t *testing.T) {
	r := content.FlatRow{
		Kind: "MODULE", Depth: 0, Id: 7, Title: "x", Type: "Module",
		LastModified: "2026-01-01", IsHidden: false, IsLocked: false, IsBroken: false,
	}
	data, err := json.Marshal(r)
	require.NoError(t, err)
	s := string(data)
	for _, want := range []string{`"kind":"MODULE"`, `"depth":0`, `"id":7`, `"title":"x"`, `"type":"Module"`, `"url":""`, `"lastModified":"2026-01-01"`, `"isHidden":false`, `"isLocked":false`, `"isBroken":false`} {
		assert.True(t, strings.Contains(s, want), "missing %s in %s", want, s)
	}
}