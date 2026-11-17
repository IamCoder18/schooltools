package ps_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/httpclient"
	"github.com/IamCoder18/schooltools/internal/ps"
)

func TestClientGetEnrollments(t *testing.T) {
	requests := make(chan *http.Request, 64)
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/90000001/enrollments", func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(loadFixture(t, "enrollments.json")))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	stu, err := c.GetEnrollments(90000001, 36)
	require.NoError(t, err)
	assert.Equal(t, "Student, Sample", stu.LastFirst)
	assert.Equal(t, int64(90000001), stu.DCID)
	assert.Equal(t, 10, stu.GradeLevel)
	assert.Equal(t, 13, len(stu.Enrollments))
	first := stu.Enrollments[0]
	assert.Equal(t, int64(96000001), first.SectionDCID)
	assert.Equal(t, "Mathematics 20-1_cohort IB", first.Section.CourseName)
	assert.Equal(t, "Teacher, Sample", first.Section.TeacherName)

	// Last request should have had Origin + Accept headers.
	var last *http.Request
	for {
		select {
		case r := <-requests:
			last = r
		case <-time.After(50 * time.Millisecond):
			goto done
		}
	}
done:
	require.NotNil(t, last)
	assert.Equal(t, "GET", last.Method)
	assert.Equal(t, "A,I,P", last.URL.Query().Get("status"))
	assert.Equal(t, "36", last.URL.Query().Get("year_id"))
	assert.Contains(t, last.Header.Get("Accept"), "application/json")
}

func TestClientGetYears(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section/student/years", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]ps.Year{
			{Abbr: "22-23", YearID: 32},
			{Abbr: "26-27", YearID: 36},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	years, err := c.GetYears(90000001)
	require.NoError(t, err)
	require.Len(t, years, 2)
	assert.Equal(t, "26-27", years[1].Abbr)
	assert.Equal(t, 36, years[1].YearID)
}

func TestClientGetStandards(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/section/standard", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "true", r.URL.Query().Get("include_comment_only"))
		assert.Equal(t, "true", r.URL.Query().Get("include_nonassignment"))
		assert.Equal(t, "1,2,3", r.URL.Query().Get("section_ids"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"standardid":1,"name":"x","subjectarea":"Math"},{"standardid":2,"name":"y","subjectarea":"ELA"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	stds, err := c.GetStandards([]int64{1, 2, 3})
	require.NoError(t, err)
	require.Len(t, stds, 2)
	assert.Equal(t, "Math", stds[0].SubjectArea)
}

func TestClientPostAddsOrigin(t *testing.T) {
	var captured *http.Request
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/standard_grade_section", func(w http.ResponseWriter, r *http.Request) {
		captured = r
		assert.Equal(t, "POST", r.Method)
		assert.Contains(t, r.Header.Get("Origin"), "127.0.0.1")
		assert.Equal(t, "application/json;charset=UTF-8", r.Header.Get("Content-Type"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"standardgradesectionid":1,"studentsdcid":90000001,"standardid":99,"sectionsdcid":1,"storecode":"T1"}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	rows, err := c.GetStandardGrades(90000001, []int64{1}, 36)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, int64(99), rows[0].StandardID)
	require.NotNil(t, captured)
}

func TestClientSurfacesNon2xx(t *testing.T) {
	// A synthetic identifier stands in for the student number; the point
	// is that nothing from the body reaches the error text.
	const bodyID = "999999999"

	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("No access to student_id " + bodyID))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
	// The body can carry identifiers, so it is summarised, never inlined.
	assert.NotContains(t, err.Error(), "No access to student_id")
	assert.NotContains(t, err.Error(), bodyID)
	// The query string carries the student DCID and is redacted too.
	assert.NotContains(t, err.Error(), "?status=")
	assert.NotContains(t, err.Error(), "year_id=")
}

func TestClientNon2xxOmitsResponseBody(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"E_NO_ENROLLMENT","message":"student has no enrollments"}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 400")
	assert.NotContains(t, err.Error(), "student has no enrollments")
}

func TestClientRejectsBadJSON(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode JSON")
}

func TestJoinCSV(t *testing.T) {
	assert.Equal(t, "", ps.JoinCSV([]string{}))
	assert.Equal(t, "a", ps.JoinCSV([]string{"a"}))
	assert.Equal(t, "a,b,c", ps.JoinCSV([]string{"a", "b", "c"}))
}

func TestStemAssignmentPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/studentSectionStemAssignments.json", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		assert.Equal(t, "92000001", q.Get("studentId"))
		assert.Equal(t, "96000001", q.Get("sectionDcid"))
		assert.Equal(t, "15400129", q.Get("standardId"))
		w.Header().Set("Content-Type", "application/json")
		// Real CBE responses are all-string. Match that here.
		_, _ = w.Write([]byte(`{"data": [{"sectionDcid":"96000001","standardId":"15400129","assignmentId":"3260086","assignmentName":"Unit 1 MC Exam","category":"OBA Tasks","dueDate":"Sep 22, 2026","actualScoreEntered":"EX1","isCollected":"0","isLate":"0","isMissing":"0","isExempt":"0","isAbsent":"0","isIncomplete":"0"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	resp, err := c.GetStemAssignment(92000001, 96000001, 15400129)
	require.NoError(t, err)
	require.Len(t, resp.Data, 1)
	assert.Equal(t, "3260086", resp.Data[0].AssignmentID)
	assert.Equal(t, "Unit 1 MC Exam", resp.Data[0].AssignmentName)
	assert.Equal(t, "EX1", resp.Data[0].ActualScoreEntered)
}

func TestSchoolCommentLimits(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/school/comment", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"schoolid":816,"schoolsdcid":235,"maxfinalgradecommentsize":2048}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	limits, err := c.GetSchoolCommentLimits()
	require.NoError(t, err)
	require.Len(t, limits, 1)
	assert.Equal(t, 2048, limits[0].MaxFinalGradeCommentSize)
	assert.Equal(t, 816, limits[0].SchoolID)
}

// loadFixture reads a JSON fixture from internal/ps/testdata. Falls back
// to an inline empty object if the file is missing so a partial checkout
// still runs.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	paths := []string{
		"internal/ps/testdata/" + name,
		"testdata/" + name,
		"./testdata/" + name,
	}
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			return string(b)
		}
	}
	return "{}"
}

// TestClientRefreshesOn401 asserts a server-side session expiry is
// recovered from rather than surfacing as a permanent opaque 403. The
// psaid cookie PowerSchool issues has no Expires, so the local validity
// check cannot see the revocation — the reauth hook is the only path
// back.
func TestClientRefreshesOn401(t *testing.T) {
	var calls int32
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dcid":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	freshJar := jar
	c.SetReauth(func() (*cookiejar.Jar, error) {
		return freshJar, nil
	})

	stu, err := c.GetEnrollments(1, 36)
	require.NoError(t, err)
	assert.Equal(t, int64(1), stu.DCID)
	assert.Equal(t, int32(2), atomic.LoadInt32(&calls))
}

// TestClientReportsFailureAfterRefresh asserts a 401 that survives a
// successful re-auth still reports the status instead of looping.
func TestClientReportsFailureAfterRefresh(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL
	c.SetReauth(func() (*cookiejar.Jar, error) { return jar, nil })

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

// TestClientSurfacesReauthFailure asserts a failed refresh reports the
// underlying login problem rather than a bare status code.
func TestClientSurfacesReauthFailure(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL
	c.SetReauth(func() (*cookiejar.Jar, error) { return nil, errors.New("INVALID_CREDS") })

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "re-authentication failed")
	assert.Contains(t, err.Error(), "INVALID_CREDS")
}

// TestClientDoesNotRefreshWithoutHook asserts a 403 without a reauth hook
// is reported as-is.
func TestClientDoesNotRefreshWithoutHook(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err := c.GetEnrollments(1, 36)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403")
}

// TestClientCollapsesConcurrentRefresh asserts a burst of 401s triggers
// exactly one re-authentication. Without the collapse, `sis assignments`
// fanning out across workers would issue one full login per in-flight
// request against the portal that just rejected the session.
func TestClientCollapsesConcurrentRefresh(t *testing.T) {
	const callers = 8
	var refreshed atomic.Bool
	var arrived atomic.Int32
	release := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		if !refreshed.Load() {
			if arrived.Add(1) == int32(callers) {
				close(release)
			}
			<-release
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"dcid":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	var reauths int32
	c.SetReauth(func() (*cookiejar.Jar, error) {
		atomic.AddInt32(&reauths, 1)
		refreshed.Store(true)
		return jar, nil
	})

	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.GetEnrollments(1, 36)
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "caller %d", i)
	}
	assert.Equal(t, int32(1), atomic.LoadInt32(&reauths),
		"a concurrent burst of 401s must trigger exactly one re-authentication")
}

func TestClientErrorRedactsStudentDCID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/xte/student/90000001/enrollments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, _ := httpclient.NewJar()
	c := ps.NewClient(jar)
	c.Base = srv.URL

	_, err := c.GetEnrollments(90000001, 36)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "90000001")
	assert.Contains(t, err.Error(), "student/<redacted>/")
}
