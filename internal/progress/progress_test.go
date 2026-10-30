package progress

import (
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	c := NewClient(jar, 42)
	c.Base = srv.URL
	return c
}

func homePage(token string) string {
	return "<html><script>localStorage.setItem('XSRF.Token','" + token + "');</script></html>"
}

func TestFetchReturnsDefaultStateRow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case r.URL.Path == "/d2l/api/le/unstable/1522695/content/userprogress/7":
			require.Equal(t, "42", r.URL.Query().Get("userId"))
			require.Equal(t, "tok", r.Header.Get("X-Csrf-Token"))
			require.Equal(t, "XMLHttpRequest", r.Header.Get("X-Requested-With"))
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":null,"IsRead":false,"NumVisits":0,"TotalTime":0,"LastVisited":null,"Visited":false,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := testClient(t, srv).Fetch("1522695", 7)
	require.NoError(t, err)
	require.Equal(t, 7, p.ObjectId)
	require.False(t, p.Visited)
	require.Nil(t, p.LastVisited)
	require.Equal(t, 0, p.TotalTime)
}

func TestUpdateSendsFullBodyWithoutIsExempt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case r.URL.Path == "/d2l/api/le/unstable/1522695/content/userprogress/":
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			for _, key := range []string{"ObjectId", "UserId", "CompletedDate", "IsRead", "NumVisits", "TotalTime", "LastVisited", "Visited", "Completed"} {
				require.Contains(t, body, key)
			}
			require.NotContains(t, body, "IsExempt")
			require.Equal(t, float64(7), body["ObjectId"])
			require.Equal(t, float64(42), body["UserId"])
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":"2026-10-01T12:00:00.000Z","IsRead":true,"NumVisits":3,"TotalTime":600,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":true}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	echo, err := testClient(t, srv).Update("1522695", UserProgress{
		ObjectId: 7, UserId: 42, CompletedDate: ptr("2026-10-01T12:00:00.000Z"),
		IsRead: true, NumVisits: 3, TotalTime: 600, Visited: true, Completed: true, IsExempt: true,
	})
	require.NoError(t, err)
	require.True(t, echo.Completed)
	require.Equal(t, 600, echo.TotalTime)
}

func TestUpdateRetriesWithFreshTokenOn403(t *testing.T) {
	var homeHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/d2l/home":
			tok := "tok1"
			if homeHits.Add(1) >= 2 {
				tok = "tok2"
			}
			_, _ = w.Write([]byte(homePage(tok)))
		case r.URL.Path == "/d2l/api/le/unstable/1522695/content/userprogress/":
			if r.Header.Get("X-Csrf-Token") != "tok2" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"Errors":[{"Message":"Forbidden"}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":1,"TotalTime":0,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := testClient(t, srv)
	echo, err := c.Update("1522695", UserProgress{ObjectId: 7, Visited: true, IsRead: true, NumVisits: 1})
	require.NoError(t, err)
	require.True(t, echo.Visited)
	require.GreaterOrEqual(t, homeHits.Load(), int32(2))
}

func TestUpdateSurfacesAPIErrorMessages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case "/d2l/api/le/unstable/1522695/content/userprogress/":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"Errors":[{"Message":"Invalid Parameters"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	_, err := testClient(t, srv).Update("1522695", UserProgress{ObjectId: 7})
	require.Error(t, err)
	apiErr, ok := err.(*APIError)
	require.True(t, ok)
	require.Equal(t, 400, apiErr.StatusCode)
	require.Equal(t, "Invalid Parameters", apiErr.Message)
}

func TestCreditTimeReportsClamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case "/d2l/api/le/unstable/1522695/content/userprogress/":
			var body updateBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			total := body.TotalTime
			if total > 31 {
				total = 31
			}
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":1,"TotalTime":` + itoa(total) + `,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	res, err := testClient(t, srv).CreditTime("1522695", UserProgress{ObjectId: 7, TotalTime: 30}, 3600, false, nil)
	require.NoError(t, err)
	require.Equal(t, 3600, res.Requested)
	require.Equal(t, 1, res.Credited)
	require.Equal(t, 31, res.After.TotalTime)
}

func TestCreditTimeWaitReposts(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case "/d2l/api/le/unstable/1522695/content/userprogress/":
			posts.Add(1)
			var body updateBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":1,"TotalTime":` + itoa(body.TotalTime) + `,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var slept []time.Duration
	sleep := func(d time.Duration) { slept = append(slept, d) }
	cur := UserProgress{ObjectId: 7, TotalTime: 0, Visited: true, LastVisited: ptr("2026-10-29T00:00:00.000Z")}
	res, err := testClient(t, srv).CreditTime("1522695", cur, 5, true, sleep)
	require.NoError(t, err)
	require.Equal(t, 5, res.Credited)
	require.Equal(t, 5, res.After.TotalTime)
	require.Equal(t, int32(1), posts.Load())
	require.Empty(t, slept)
}

func TestCreditTimeWaitSleepsGap(t *testing.T) {
	var posts atomic.Int32
	var last atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/d2l/home":
			_, _ = w.Write([]byte(homePage("tok")))
		case "/d2l/api/le/unstable/1522695/content/userprogress/":
			posts.Add(1)
			var body updateBody
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			echo := body.TotalTime
			if prev := int(last.Load()); echo > prev+1 {
				echo = prev + 1
			}
			last.Store(int64(echo))
			_, _ = w.Write([]byte(`{"ObjectId":7,"UserId":42,"CompletedDate":null,"IsRead":true,"NumVisits":1,"TotalTime":` + itoa(echo) + `,"LastVisited":"2026-10-30T00:00:00.000Z","Visited":true,"Completed":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var slept []time.Duration
	sleep := func(d time.Duration) { slept = append(slept, d) }
	cur := UserProgress{ObjectId: 7, TotalTime: 0, Visited: true}
	res, err := testClient(t, srv).CreditTime("1522695", cur, 3, true, sleep)
	require.NoError(t, err)
	require.Equal(t, 3, res.Credited)
	require.Equal(t, 3, res.After.TotalTime)
	require.Equal(t, int32(3), posts.Load())
	require.Len(t, slept, 2)
	require.Equal(t, 2500*time.Millisecond, slept[0])
	require.Equal(t, 1500*time.Millisecond, slept[1])
}

func TestParseAPIErrorMessage(t *testing.T) {
	require.Equal(t, "Forbidden", parseAPIErrorMessage(`{"Errors":[{"Message":"Forbidden"}]}`))
	require.Equal(t, "Invalid Parameters", parseAPIErrorMessage(`{"title":"Bad Request","detail":"Invalid Parameters"}`))
	require.Equal(t, "Bad Request", parseAPIErrorMessage(`{"title":"Bad Request"}`))
	require.Equal(t, "not json", parseAPIErrorMessage("not json"))
}

func TestParseUserID(t *testing.T) {
	id, err := ParseUserID(" 782002 ")
	require.NoError(t, err)
	require.Equal(t, 782002, id)
	_, err = ParseUserID("abc")
	require.Error(t, err)
}

func itoa(n int) string {
	return strconv.Itoa(n)
}
