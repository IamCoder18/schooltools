package progress

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/content"
)

func bulkFixture(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/content/userprogress/"):
			var body string
			switch {
			case strings.HasSuffix(r.URL.Path, "/1"):
				body = `{"ObjectId":1,"UserId":42,"CompletedDate":null,"IsRead":false,"NumVisits":0,"TotalTime":0,"LastVisited":null,"Visited":false,"Completed":false}`
			case strings.HasSuffix(r.URL.Path, "/2"):
				body = `{"ObjectId":2,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":5,"TotalTime":100,"LastVisited":"2026-10-01T00:00:00.000Z","Visited":true,"Completed":false}`
			case strings.HasSuffix(r.URL.Path, "/3"):
				body = `{"ObjectId":3,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":2,"TotalTime":10,"LastVisited":"2026-10-01T00:00:00.000Z","Visited":true,"Completed":false}`
			default:
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodPost && r.URL.Path == "/d2l/api/le/unstable/1522695/content/userprogress/":
			posts.Add(1)
			var body updateBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			if body.ObjectId == 2 {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`boom`))
				return
			}
			_, _ = w.Write([]byte(`{"ObjectId":` + itoa(body.ObjectId) + `,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":` + itoa(body.NumVisits) + `,"TotalTime":0,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &posts
}

func bulkTopics(string) ([]content.TocTopic, error) {
	return []content.TocTopic{
		{TopicId: 1, Title: "Fresh"},
		{TopicId: 2, Title: "Failing write"},
		{TopicId: 3, Title: "Already at target"},
	}, nil
}

func bulkClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := NewClient(jar, 42)
	c.Base = srv.URL
	return c
}

func TestRunWritesOnlyChangedTopics(t *testing.T) {
	srv, posts := bulkFixture(t)
	defer srv.Close()

	res, err := Run(BulkOptions{
		Client:    bulkClient(t, srv),
		Plan:      PlanOptions{SetVisits: intPtr(2)},
		CourseIDs: []string{"1522695"},
		Topics:    bulkTopics,
	})
	require.NoError(t, err)
	require.Equal(t, 3, res.TopicsChecked)
	require.Equal(t, 2, res.TopicsChanged)
	require.Equal(t, 1, res.TopicsWritten)
	require.Equal(t, 1, res.TopicsFailed)
	require.Equal(t, int32(2), posts.Load())

	require.Len(t, res.Failures, 1)
	require.Equal(t, 2, res.Failures[0].TopicID)
	require.Len(t, res.Changes, 1)
	require.Equal(t, 1, res.Changes[0].After.ObjectId)
}

func TestRunDryRunWritesNothing(t *testing.T) {
	srv, posts := bulkFixture(t)
	defer srv.Close()

	res, err := Run(BulkOptions{
		Client:    bulkClient(t, srv),
		Plan:      PlanOptions{SetVisits: intPtr(2)},
		DryRun:    true,
		CourseIDs: []string{"1522695"},
		Topics:    bulkTopics,
	})
	require.NoError(t, err)
	require.Equal(t, 2, res.TopicsChanged)
	require.Equal(t, 0, res.TopicsWritten)
	require.Equal(t, int32(0), posts.Load())
	require.Len(t, res.Changes, 2)
}

func TestRunNoRulesMeansNoWrites(t *testing.T) {
	srv, posts := bulkFixture(t)
	defer srv.Close()

	res, err := Run(BulkOptions{
		Client:    bulkClient(t, srv),
		CourseIDs: []string{"1522695"},
		Topics:    bulkTopics,
	})
	require.NoError(t, err)
	require.Equal(t, 0, res.TopicsChanged)
	require.Equal(t, int32(0), posts.Load())
}

func TestRunUsesCourseLister(t *testing.T) {
	srv, _ := bulkFixture(t)
	defer srv.Close()

	var listed int
	res, err := Run(BulkOptions{
		Client: bulkClient(t, srv),
		Plan:   PlanOptions{MinVisits: 1},
		Courses: func() ([]string, error) {
			listed++
			return []string{"1522695"}, nil
		},
		Topics: bulkTopics,
	})
	require.NoError(t, err)
	require.Equal(t, 1, listed)
	require.Equal(t, 1, res.CoursesScanned)
}

func TestPacerDisablesAtZero(t *testing.T) {
	p := newPacer(0)
	var slept []int
	p.sleep = func(d time.Duration) { slept = append(slept, int(d)) }
	p.wait()
	require.Empty(t, slept)
}
