package ps

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestExtractDCIDFromDashboard(t *testing.T) {
	tests := []struct {
		name string
		html string
		want int64
	}{
		{
			name: "CBE dashboard with credentials link",
			html: `<a href="/guardian/credentials.html?frn=00190000001">My Profile</a>`,
			want: 90000001,
		},
		{
			name: "FRN with 2-digit table code (e.g. section-style)",
			html: `<a href="/guardian/credentials.html?frn=001290000001">X</a>`,
			want: 90000001,
		},
		{
			name: "no credentials link",
			html: `<html>no link here</html>`,
			want: 0,
		},
		{
			name: "credentials link with non-numeric FRN",
			html: `<a href="/guardian/credentials.html?frn=00abc">X</a>`,
			want: 0,
		},
		{
			name: "credentials link with too-short FRN (< 8 digits)",
			html: `<a href="/guardian/credentials.html?frn=0055282">X</a>`,
			want: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExtractDCIDFromDashboard(tc.html))
		})
	}
}

func TestExtractDCIDFromDashboardRealFixture(t *testing.T) {
	// Read a dashboard body and confirm we extract the correct DCID.
	body := strings.Builder{}
	body.WriteString(`<a href="/guardian/credentials.html?frn=00190000001">My Profile</a>`)
	body.WriteString(`<a href="/guardian/home.html">Home</a>`)
	body.WriteString(`<a href="/guardian/grades.html">Grades</a>`)
	body.WriteString(`<a href="/guardian/attendance.html">Attendance</a>`)
	got := ExtractDCIDFromDashboard(body.String())
	assert.Equal(t, int64(90000001), got)
}
