package cmd

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/ps"
)

// TestStemRowsSkipsSentinelCells asserts cells with no assignmentId are
// dropped: the legacy endpoint returns a trailing {} sentinel for every
// cell without a posted assignment.
func TestStemRowsSkipsSentinelCells(t *testing.T) {
	rows := stemRows([]ps.StemAssignment{
		{AssignmentID: "", AssignmentName: ""},
		{
			AssignmentID:       "77",
			SectionDCID:        "96000001",
			StandardID:         "15400129",
			AssignmentName:     "Unit 1 quiz",
			Category:           "Quiz",
			DueDate:            "2026-10-01",
			ActualScoreEntered: "EX1",
			IsMissing:          "1",
			IsCollected:        "0",
		},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, int64(77), rows[0].AssignmentID)
	assert.Equal(t, int64(96000001), rows[0].SectionDCID)
	assert.Equal(t, "EX1", rows[0].Score)
	assert.True(t, rows[0].IsMissing)
	assert.False(t, rows[0].IsCollected)
}

// TestStemRowsCoercesStringIDs asserts the legacy all-string payload is
// coerced to int64, and that unparseable values degrade to zero rather
// than panicking.
func TestStemRowsCoercesStringIDs(t *testing.T) {
	rows := stemRows([]ps.StemAssignment{
		{AssignmentID: "notanumber", SectionDCID: "alsonot", StandardID: "nope"},
	})
	require.Len(t, rows, 1)
	assert.Equal(t, int64(0), rows[0].AssignmentID)
	assert.Equal(t, int64(0), rows[0].SectionDCID)
}

// TestFetchStemCellsIsolatesFailures asserts one failing cell does not
// abort the run or discard the cells that succeeded — the behaviour the
// old serial loop got wrong when it returned on the first error. The
// handler blocks until two requests are provably in flight, so the
// concurrency assertion is deterministic rather than timing-dependent.
func TestFetchStemCellsIsolatesFailures(t *testing.T) {
	const wantOverlap = 2
	release := make(chan struct{})
	arrived := make(chan struct{}, 8)
	var timedOut atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/guardian/studentSectionStemAssignments.json", func(w http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		select {
		case <-release:
		case <-time.After(5 * time.Second):
			timedOut.Store(true)
		}

		if r.URL.Query().Get("standardId") == "2" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"assignmentId":"5","sectionDcid":"1","standardId":"1","assignmentName":"A"}]}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := httpclient.NewJar()
	require.NoError(t, err)
	c := ps.NewClient(jar)
	c.Base = srv.URL

	cells := []sisCell{
		{sectionDCID: 1, standardID: 1},
		{sectionDCID: 1, standardID: 2},
		{sectionDCID: 1, standardID: 3},
		{sectionDCID: 1, standardID: 4},
	}
	done := make(chan struct{})
	var out []sisAssignment
	var failures map[sisCell]error
	go func() {
		out, failures = fetchStemCells(c, 92000001, cells)
		close(done)
	}()

	for i := 0; i < wantOverlap; i++ {
		select {
		case <-arrived:
		case <-time.After(5 * time.Second):
			close(release)
			<-done
			t.Fatalf("only %d of %d requests overlapped; the fan-out is not concurrent", i, wantOverlap)
		}
	}
	close(release)
	<-done

	require.False(t, timedOut.Load(), "handler timed out waiting for release")
	assert.Len(t, failures, 1)
	assert.Contains(t, failures, cells[1])
	assert.Len(t, out, 3, "the three healthy cells should still be returned")
}

// TestProficiencyFor asserts score tokens map to the published label and
// nominal percentage, and that unknown or empty tokens stay unlabelled.
func TestProficiencyFor(t *testing.T) {
	label, percent := proficiencyFor("ex2")
	assert.Equal(t, "Exemplary 2", label)
	require.NotNil(t, percent)
	assert.Equal(t, 100.0, *percent)

	label, percent = proficiencyFor(" PR1 ")
	assert.Equal(t, "Proficient 1", label)
	require.NotNil(t, percent)
	assert.Equal(t, 75.0, *percent)

	label, percent = proficiencyFor("")
	assert.Empty(t, label)
	assert.Nil(t, percent)

	label, percent = proficiencyFor("ZZ9")
	assert.Empty(t, label)
	assert.Nil(t, percent)
}

// TestFormatScore asserts the table cell falls back to the raw token when
// the level is unknown, and to a dash when there is no score yet.
func TestFormatScore(t *testing.T) {
	assert.Equal(t, "—", formatScore("", ""))
	assert.Equal(t, "EX1", formatScore("EX1", ""))
	assert.Equal(t, "EX1 (Exemplary 1)", formatScore("EX1", "Exemplary 1"))
}

// TestAssignmentFlags asserts only the set flags are listed, in a stable
// order, so the table can append them to the assignment name.
func TestAssignmentFlags(t *testing.T) {
	assert.Empty(t, assignmentFlags(sisAssignment{}))
	assert.Equal(t, "collected,missing", assignmentFlags(sisAssignment{IsCollected: true, IsMissing: true}))
	assert.Equal(t, "late,exempt", assignmentFlags(sisAssignment{IsLate: true, IsExempt: true}))
}
