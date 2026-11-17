package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aarav/schooltools/internal/ps"
)

func TestSISStandardGradeRows(t *testing.T) {
	percent := 97.5
	rows := sisStandardGradeRows([]ps.StandardGrade{
		{
			ID:           1,
			StudentDCID:  2,
			StandardID:   3,
			SectionDCID:  4,
			StoreCode:    "T1",
			Grade:        "EX1",
			Percent:      &percent,
			IsIncomplete: true,
			LastUpdated:  "2026-11-01 10:00:00",
			IsEnrolled:   true,
		},
		{
			ID:          5,
			StudentDCID: 2,
			StandardID:  6,
			SectionDCID: 4,
			StoreCode:   "S1",
			IsEnrolled:  true,
		},
	}, map[int64]string{4: "Science 10"})

	require.Len(t, rows, 2)
	assert.Equal(t, "Science 10", rows[0].Course)
	assert.Equal(t, "EX1", rows[0].Grade)
	require.NotNil(t, rows[0].Percent)
	assert.Equal(t, 97.5, *rows[0].Percent)
	assert.Equal(t, []string{"incomplete"}, rows[0].Flags)
	assert.Equal(t, "2026-11-01 10:00:00", rows[0].LastUpdated)

	assert.Empty(t, rows[1].Grade)
	assert.Nil(t, rows[1].Percent)
	assert.Empty(t, rows[1].Flags)
}

func TestSISStandardGradeRowsFlags(t *testing.T) {
	rows := sisStandardGradeRows([]ps.StandardGrade{
		{ID: 1, IsExempt: true, IsLocked: true, IsEnrolled: false},
	}, nil)
	require.Len(t, rows, 1)
	assert.Equal(t, []string{"exempt", "locked", "not_enrolled"}, rows[0].Flags)
}

func TestFormatPercent(t *testing.T) {
	assert.Equal(t, "—", formatPercent(nil))

	whole := 100.0
	assert.Equal(t, "100", formatPercent(&whole))

	fractional := 97.5
	assert.Equal(t, "97.5", formatPercent(&fractional))

	precise := 98.33
	assert.Equal(t, "98.33", formatPercent(&precise))
}

func TestGradeOrDash(t *testing.T) {
	assert.Equal(t, "—", gradeOrDash(""))
	assert.Equal(t, "EX2", gradeOrDash("EX2"))
}
