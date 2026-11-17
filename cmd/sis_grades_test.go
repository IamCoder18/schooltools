package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/ps"
)

func TestSISSectionRowsFlattens(t *testing.T) {
	rows := sisSectionRows([]ps.Enrollment{
		{
			SectionDCID:  1533424,
			Status:       "A",
			EnrolledLate: true,
			Section: ps.SectionInfo{
				SectionNum:       "211",
				Expression:       "R1(A)",
				CourseName:       "Mathematics 20-1_cohort IB",
				CourseNum:        "MAT2791H",
				TeacherName:      "Weiss, Tannis R",
				TermName:         "Semester 2",
				TermAbbreviation: "S2",
			},
		},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, int64(1533424), rows[0].SectionDCID)
	assert.Equal(t, "A", rows[0].Status)
	assert.True(t, rows[0].EnrolledLate)
	assert.Equal(t, "Mathematics 20-1_cohort IB", rows[0].CourseName)
	assert.Equal(t, "Weiss, Tannis R", rows[0].Teacher)
	assert.Equal(t, "S2", rows[0].TermAbbr)
}

func TestMarkOrDash(t *testing.T) {
	assert.Equal(t, "—", markOrDash(""))
	assert.Equal(t, "97", markOrDash("97"))
}
