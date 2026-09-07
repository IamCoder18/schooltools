package content_test

import (
	"testing"

	"github.com/aarav/schooltools/internal/content"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTopicRefCanonical(t *testing.T) {
	course, topic, err := content.ParseTopicRef("1527886/topics/19278283")
	require.NoError(t, err)
	assert.Equal(t, "1527886", course)
	assert.Equal(t, 19278283, topic)
}

func TestParseTopicRefShortForm(t *testing.T) {
	course, topic, err := content.ParseTopicRef("1527886/19278283")
	require.NoError(t, err)
	assert.Equal(t, "1527886", course)
	assert.Equal(t, 19278283, topic)
}

func TestParseTopicRefWhitespaceForm(t *testing.T) {
	course, topic, err := content.ParseTopicRef("1527886 topics 19278283")
	require.NoError(t, err)
	assert.Equal(t, "1527886", course)
	assert.Equal(t, 19278283, topic)
}

func TestParseTopicRefRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"   ",
		"1527886",
		"1527886/topics",
		"1527886/topics/notanumber",
		"topics/19278283",
		"1527886/files/19278283",
	} {
		_, _, err := content.ParseTopicRef(bad)
		assert.Error(t, err, "expected error for %q", bad)
	}
}

func TestTopicIsFileAndIsLink(t *testing.T) {
	url := "https://example.com/foo.pdf"
	cases := []struct {
		name      string
		topic     content.Topic
		wantFile  bool
		wantLink  bool
		wantURL   string
	}{
		{
			name: "explicit File type",
			topic: content.Topic{
				TypeIdentifier: "File",
				TopicType:      1,
				Url:            &url,
			},
			wantFile: true,
			wantURL:  url,
		},
		{
			name: "bare topic type 1 defaults to File",
			topic: content.Topic{
				Url:       &url,
				TopicType: 1,
			},
			wantFile: true,
			wantURL:  url,
		},
		{
			name: "explicit Link type",
			topic: content.Topic{
				TypeIdentifier: "Link",
				TopicType:      2,
				Url:            &url,
			},
			wantLink: true,
		},
		{
			name: "bare topic type 2 defaults to Link",
			topic: content.Topic{
				TopicType: 2,
				Url:       &url,
			},
			wantLink: true,
		},
		{
			name:     "no url means no file url",
			topic:    content.Topic{TypeIdentifier: "File", TopicType: 1},
			wantFile: true,
			wantURL:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.wantFile, tc.topic.IsFile())
			assert.Equal(t, tc.wantLink, tc.topic.IsLink())
			assert.Equal(t, tc.wantURL, tc.topic.FileURL())
		})
	}
}

func TestTopicFileURLNonFileIsEmpty(t *testing.T) {
	url := "https://example.com/"
	tp := content.Topic{TypeIdentifier: "Link", TopicType: 2, Url: &url}
	assert.False(t, tp.IsFile())
	assert.Equal(t, "", tp.FileURL())
}
