package tree

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aarav/schooltools/internal/content"
)

func ptr(s string) *string { return &s }

func sampleToc() []content.TocModule {
	return []content.TocModule{
		{
			ModuleId:        1,
			Title:           "Week 1",
			LastModifiedDate: "2026-01-02T03:04:05Z",
			Topics: []content.TocTopic{
				{
					TopicId:         100,
					Title:           "Syllabus",
					TypeIdentifier:  "File",
					LastModifiedDate: "2026-01-02T03:04:05Z",
				},
				{
					TopicId:         101,
					Title:           "Course Site",
					TopicType:       2,
					Url:             ptr("https://example.com"),
					LastModifiedDate: "2026-01-03T03:04:05Z",
				},
			},
		},
		{
			ModuleId: 2,
			Title:    "Week 2",
			Modules: []content.TocModule{
				{
					ModuleId:        21,
					Title:           "Readings",
					IsHidden:        true,
					LastModifiedDate: "2026-01-04T03:04:05Z",
					Topics: []content.TocTopic{
						{
							TopicId:         200,
							Title:           "Chapter 1",
							TypeIdentifier:  "File",
							IsBroken:        true,
							LastModifiedDate: "2026-01-04T03:04:05Z",
						},
					},
				},
			},
		},
	}
}

func TestRenderTOC_Empty(t *testing.T) {
	assert.Equal(t, "", RenderTOC(nil, Options{}))
}

func TestRenderTOC_TitleOnly(t *testing.T) {
	out := RenderTOC(sampleToc(), Options{})
	require.NotEmpty(t, out)

	lines := strings.Split(out, "\n")
	require.GreaterOrEqual(t, len(lines), 5)

	// All node titles must be present somewhere in the rendered tree.
	for _, want := range []string{"Week 1", "Week 2", "Syllabus", "Course Site", "Readings", "Chapter 1"} {
		assert.Contains(t, out, want)
	}

	// treeprint uses ├── and └── connectors.
	assert.Contains(t, out, "├──")
	assert.Contains(t, out, "└──")
	assert.Contains(t, out, "│")
}

func TestRenderTOC_ShowFlagsAndKind(t *testing.T) {
	out := RenderTOC(sampleToc(), Options{
		ShowKind:  true,
		ShowFlags: true,
	})

	assert.Contains(t, out, "▣ Module")
	assert.Contains(t, out, "▢ Topic")
	assert.Contains(t, out, "[hidden]")
	assert.Contains(t, out, "[broken]")
}

func TestRenderTOC_ShowType(t *testing.T) {
	out := RenderTOC(sampleToc(), Options{ShowType: true})

	// Chapter 1 has TypeIdentifier == "File"; Course Site falls back to "Link".
	assert.Contains(t, out, "[File]")
	assert.Contains(t, out, "[Link]")
}

func TestRenderTOC_ShowDate(t *testing.T) {
	out := RenderTOC(sampleToc(), Options{ShowDate: true})

	assert.Contains(t, out, "[2026-01-02T03:04]")
	assert.Contains(t, out, "[2026-01-04T03:04]")
}

func TestRenderTOC_NestedConnectorsCorrect(t *testing.T) {
	out := RenderTOC(sampleToc(), Options{})

	// Nested module "Readings" must have both the parent's connector
	// and its own connector — i.e. "│   " followed by "└── " or "├── ".
	assert.Contains(t, out, "│   ")
	// Specifically, Readings is the only child of Week 2, so it should
	// be drawn with └── at the second level.
	assert.Regexp(t, `(?m)^└── Week 2$`, out)
	assert.Regexp(t, `(?m)^    └── Readings$`, out)
}

func TestLines_Empty(t *testing.T) {
	assert.Empty(t, Lines(nil))
	assert.Empty(t, Lines([]content.TocModule{}))
}

func TestLines_OrderMatchesFlattenToc(t *testing.T) {
	mods := sampleToc()
	lines := Lines(mods)
	flat := content.FlattenToc(mods)

	require.Len(t, lines, len(flat),
		"line count must match FlattenToc's DFS row count")

	for i, want := range flat {
		assert.Equal(t, want.Title, lines[i].Title,
			"line %d title must match FlattenToc row %d (%q vs %q)",
			i, i, lines[i].Title, want.Title)
	}
}

func TestLines_PrefixesAndDepths(t *testing.T) {
	mods := sampleToc()
	lines := Lines(mods)

	// Expected DFS:
	//   0 Week 1              (depth 0, last=false)  ├── Week 1
	//   1 Syllabus            (depth 1, last=false)  │   ├── Syllabus
	//   2 Course Site         (depth 1, last=true)   │   └── Course Site
	//   3 Week 2              (depth 0, last=true)   └── Week 2
	//   4 Readings            (depth 1, last=true)       └── Readings
	//   5 Chapter 1           (depth 2, last=true)           └── Chapter 1
	type want struct {
		prefix string
		title  string
		depth  int
	}
	expect := []want{
		{"├── ", "Week 1", 0},
		{"│   ├── ", "Syllabus", 1},
		{"│   └── ", "Course Site", 1},
		{"└── ", "Week 2", 0},
		{"    └── ", "Readings", 1},
		{"        └── ", "Chapter 1", 2},
	}
	require.Len(t, lines, len(expect))
	for i, w := range expect {
		assert.Equal(t, w.prefix, lines[i].Prefix, "line %d prefix", i)
		assert.Equal(t, w.title, lines[i].Title, "line %d title", i)
		assert.Equal(t, w.depth, lines[i].Depth, "line %d depth", i)
	}
}

func TestLines_BareTitles_NoLabels(t *testing.T) {
	// Lines() must produce bare titles (no ▣/▢ markers, no [date], no
	// flags) so they can be matched cleanly to FlattenToc rows.
	mods := sampleToc()
	lines := Lines(mods)
	for _, l := range lines {
		assert.NotContains(t, l.Title, "▣")
		assert.NotContains(t, l.Title, "▢")
		assert.NotContains(t, l.Title, "[")
	}
}
