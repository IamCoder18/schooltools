package ps

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aarav/schooltools/internal/httpclient"
)

const marksPage = `<html><body><table>
<tr><th>Exp</th><th>Course</th><th>T1</th><th>T2</th><th>S1</th><th>T3</th><th>T4</th><th>S2</th><th>Y1</th><th>Absences</th><th>Lates</th></tr>
<tr class="center" id="ccid_20470974">
<td>R1(A)</td>
<td class="show0">[LastWeekThisWeekDays]</td>
<td align="left">Science 10_cohort IB OB&nbsp;<a title="Section Teachers" href="#"> <img src="x"/></a><br><a href="teacherinfo.html" title="Details about Graham, Debbie M">E</a>&nbsp;<a href="mailto:DeGraham@cbe.ab.ca">Email Graham, Debbie M</a>&nbsp;<span class="display-flex"><span>-&nbsp;Rm:</span><span>&nbsp;278 Scienc</span></span></td>
<td class="cbeGradeLink"><a href="#">94</a></td>
<td class="notInSession">&nbsp;<span class="screen_readers_only">Not available</span></td>
<td class="cbeGradeLink"><a href="#">97</a></td>
<td class="notInSession">&nbsp;<span class="screen_readers_only">Not available</span></td>
<td class="notInSession">&nbsp;<span class="screen_readers_only">Not available</span></td>
<td class="notInSession">&nbsp;<span class="screen_readers_only">Not available</span></td>
<td class="notInSession">&nbsp;<span class="screen_readers_only">Not available</span></td>
<td class="show0">0</td>
<td class="show0">0</td>
</tr>
<tr class="center" id="ccid_20470975">
<td>R2(A)</td>
<td class="show0">[LastWeekThisWeekDays]</td>
<td align="left">Mathematics 10C&nbsp;<a href="mailto:x">Email Hor, Janice</a></td>
<td>-</td>
<td class="notInSession">Not available</td>
<td class="cbeGradeLink"><a href="#">[ i ]</a></td>
<td class="notInSession">Not available</td>
<td class="notInSession">Not available</td>
<td class="notInSession">Not available</td>
<td class="notInSession">Not available</td>
<td class="show0">0</td>
<td class="show0">0</td>
</tr>
</table></body></html>`

func TestParseCourseMarks(t *testing.T) {
	got, err := ParseCourseMarks(strings.NewReader(marksPage))
	require.NoError(t, err)
	require.Len(t, got, 2)

	first := got[0]
	assert.Equal(t, "R1(A)", first.Expression)
	assert.Equal(t, "Science 10_cohort IB OB", first.Course)
	assert.Equal(t, "Graham, Debbie M", first.Teacher)
	assert.Equal(t, "94", first.MarkFor("T1"))
	assert.Equal(t, "", first.MarkFor("T2"))
	assert.Equal(t, "97", first.MarkFor("S1"))
	assert.Equal(t, "", first.MarkFor("Y1"))

	second := got[1]
	assert.Equal(t, "Mathematics 10C", second.Course)
	assert.Equal(t, "", second.MarkFor("T1"))
	assert.Equal(t, "[ i ]", second.MarkFor("S1"))
}

func TestMarkForMissingTerm(t *testing.T) {
	cm := CourseMark{Course: "X", Marks: []TermMark{{Term: "T1", Mark: "90"}}}
	assert.Equal(t, "", cm.MarkFor("S1"))
	assert.Equal(t, "90", cm.MarkFor("T1"))
}

func TestClientGetCourseMarks(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/grades.html", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(marksPage))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := NewClient(jar)
	c.Base = srv.URL

	got, err := c.GetCourseMarks()
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "94", got[0].MarkFor("T1"))
}

func TestClientGetCourseMarksNon2xx(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/grades.html", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := NewClient(jar)
	c.Base = srv.URL

	_, err = c.GetCourseMarks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

func TestClientGetCourseMarksStaleSession(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/grades.html", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body>please sign in</body></html>`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := NewClient(jar)
	c.Base = srv.URL

	_, err = c.GetCourseMarks()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "likely expired")
	assert.Contains(t, err.Error(), "schooltools sis login")
}
