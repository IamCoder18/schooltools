package cmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func sampleAward() Award {
	return Award{
		Id:            "77",
		Title:         "Robotics Participation",
		Description:   "<p>For finishing the <b>bumper</b> challenge.</p>",
		IssuedDate:    "2026-10-02",
		ExpiryDate:    "2027-10-02",
		AwardType:     "Badge",
		CertificateId: "55",
		OrgUnitName:   "Robotics 10",
	}
}

func TestAwardRows(t *testing.T) {
	withUTCLocal(t)
	rows := awardRows(sampleAward())
	keys := make([]string, len(rows))
	for i, r := range rows {
		keys[i] = r.Key
	}
	require.Equal(t, []string{"Type", "Issued", "Expires", "Course"}, keys)
	require.Equal(t, "Badge", rows[0].Value)
	require.Equal(t, "Fri, Oct 2 2026", rows[1].Value)
	require.Equal(t, "Sat, Oct 2 2027", rows[2].Value)
	require.Equal(t, "Robotics 10", rows[3].Value)
}

func TestAwardRowsMinimal(t *testing.T) {
	withUTCLocal(t)
	rows := awardRows(Award{IssuedDate: "2026-10-02"})
	require.Len(t, rows, 1)
	require.Equal(t, "Issued", rows[0].Key)
	require.Equal(t, "Fri, Oct 2 2026", rows[0].Value)
}

func TestAwardCourseLabel(t *testing.T) {
	require.Equal(t, "Robotics 10", awardCourseLabel(Award{OrgUnitName: "Robotics 10", OrgUnitId: 5}))
	require.Equal(t, "org 5", awardCourseLabel(Award{OrgUnitId: 5}))
	require.Equal(t, "", awardCourseLabel(Award{}))
}

func TestAwardPanel(t *testing.T) {
	out := awardPanel(sampleAward())
	require.Contains(t, out, "Robotics Participation")
	require.Contains(t, out, "Description")
	require.Contains(t, out, "For finishing the bumper challenge.")
	require.NotContains(t, out, "<p>")
	require.Contains(t, out, "schooltools award download 55")
}

func TestAwardDescriptionEmpty(t *testing.T) {
	require.Equal(t, "", awardDescription(Award{}))
}
