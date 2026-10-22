package cmd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/content"
)

func TestTopicReportFileTopic(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	u := "/content/enforced/3000001-x/Unit%201.pdf"
	topic := content.Topic{
		TopicId:          42,
		Title:            "Unit 1 Notes",
		TypeIdentifier:   "File",
		Url:              &u,
		FileName:         "Unit 1.pdf",
		LastModifiedDate: "2024-01-02T15:04:05.000",
		IsBroken:         true,
	}
	doc := map[string]any{
		"StartDate": "2026-09-01T00:00:00.000",
		"EndDate":   "2026-09-30T23:59:59.000",
	}

	got := topicReport("3000001", topic, doc)

	require.Contains(t, got, "Unit 1 Notes")
	require.Contains(t, got, "schooltools download --course 3000001 42")
	require.Contains(t, got, "Unit 1.pdf")
	require.Contains(t, got, u)
	require.Regexp(t, `Start\s+\w+, \w+ \d+ 2026`, got)
	require.Regexp(t, `End\s+\w+, \w+ \d+ 2026`, got)
	require.Contains(t, got, "Broken")
	require.Contains(t, got, "Next")
	require.Regexp(t, `Broken\s+yes`, got)
	require.Regexp(t, `Hidden\s+no`, got)
	require.NotContains(t, got, "true")
}

func TestTopicReportLinkTopic(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	u := "https://example.org/lesson"
	topic := content.Topic{TopicId: 7, Title: "Video", TypeIdentifier: "Link", Url: &u, TopicType: 2}

	got := topicReport("3000001", topic, nil)

	require.Contains(t, got, "Video")
	require.Contains(t, got, "Link")
	require.Contains(t, got, "https://example.org/lesson")
	require.NotContains(t, got, "schooltools download")
}

func TestTopicReportLinkWithoutURL(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	topic := content.Topic{TopicId: 8, Title: "Broken link", TypeIdentifier: "Link", TopicType: 2}

	got := topicReport("3000001", topic, nil)

	require.Contains(t, got, "No URL recorded for this link.")
	require.NotContains(t, got, "schooltools download")
}

func TestTopicReportMissingDatesUseDash(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	topic := content.Topic{TopicId: 3, Title: "Syllabus", TypeIdentifier: "File"}

	got := topicReport("3000001", topic, map[string]any{})

	require.Regexp(t, `Start\s+—`, got)
	require.Regexp(t, `End\s+—`, got)
	require.Regexp(t, `Updated\s+—`, got)
	require.Regexp(t, `URL\s+—`, got)
}

func TestContentTableRowsTruncatesLongTitles(t *testing.T) {
	rows := []content.FlatRow{{Id: 1, Kind: "TOPIC", Title: strings.Repeat("very long title ", 20)}}
	widths := contentTableWidths()

	got := contentTableRows(rows, widths)

	require.Len(t, got, 1)
	require.LessOrEqual(t, len([]rune(got[0][5])), widths[5]-2)
	require.Contains(t, got[0][5], "…")
}

func TestDocStringAndYesNo(t *testing.T) {
	doc := map[string]any{"StartDate": "2026-01-01", "EndDate": nil}
	require.Equal(t, "2026-01-01", docString(doc, "StartDate"))
	require.Equal(t, "", docString(doc, "EndDate"))
	require.Equal(t, "", docString(nil, "StartDate"))
	require.Equal(t, "yes", yesNo(true))
	require.Equal(t, "no", yesNo(false))
}

func TestTopicTypeLabel(t *testing.T) {
	require.Equal(t, "File", topicTypeLabel(content.Topic{TypeIdentifier: "File"}))
	require.Equal(t, "File", topicTypeLabel(content.Topic{TopicType: 1}))
	require.Equal(t, "Link", topicTypeLabel(content.Topic{TopicType: 2}))
	require.Equal(t, "Content link", topicTypeLabel(content.Topic{TopicType: 3}))
	require.Equal(t, "—", topicTypeLabel(content.Topic{}))
	require.Equal(t, "Other (type 9)", topicTypeLabel(content.Topic{TopicType: 9}))
}
