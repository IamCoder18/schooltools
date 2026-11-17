package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRealSubmissionsDropsPlaceholder(t *testing.T) {
	items := []Submission{
		{Id: 0, FolderId: 0, EntityType: "", EntityId: 0, SubmittedDate: "", SubmittedText: ""},
		{Id: 7, FolderId: 3, EntityType: "user", EntityId: 9, SubmittedDate: "2026-11-01T12:00:00Z", SubmittedText: "done"},
	}
	got := realSubmissions(items)
	require.Len(t, got, 1)
	assert.Equal(t, 7, got[0].Id)
	assert.Equal(t, "done", got[0].SubmittedText)
}

func TestRealSubmissionsEmpty(t *testing.T) {
	assert.Empty(t, realSubmissions(nil))
	assert.Empty(t, realSubmissions([]Submission{}))
}

func TestIsPlaceholderSubmission(t *testing.T) {
	assert.True(t, isPlaceholderSubmission(Submission{}))
	assert.False(t, isPlaceholderSubmission(Submission{Id: 1}))
	assert.False(t, isPlaceholderSubmission(Submission{FolderId: 1}))
	assert.False(t, isPlaceholderSubmission(Submission{SubmittedDate: "2026-01-01"}))
}
