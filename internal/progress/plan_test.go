package progress

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func ptr(s string) *string { return &s }

func intPtr(n int) *int { return &n }

func TestPlanMarkUnvisited(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	fresh := UserProgress{ObjectId: 7, UserId: 42}

	ch := Plan(fresh, PlanOptions{MarkUnvisited: MarkVisited}, now)
	require.Equal(t, "mark-visited", ch.Action)
	require.True(t, ch.After.Visited)
	require.True(t, ch.After.IsRead)
	require.Equal(t, 1, ch.After.NumVisits)
	require.False(t, ch.After.Completed)

	ch = Plan(fresh, PlanOptions{MarkUnvisited: MarkCompleted}, now)
	require.Equal(t, "mark-completed", ch.Action)
	require.True(t, ch.After.Completed)
	require.NotNil(t, ch.After.CompletedDate)
	require.Equal(t, "2026-10-30T12:00:00.000Z", *ch.After.CompletedDate)
	require.Equal(t, 1, ch.After.NumVisits)

	visited := UserProgress{ObjectId: 7, UserId: 42, Visited: true, IsRead: true, NumVisits: 3}
	ch = Plan(visited, PlanOptions{MarkUnvisited: MarkCompleted}, now)
	require.Equal(t, "", ch.Action)
	require.True(t, Equal(visited, ch.After))
}

func TestPlanSetVisitsReduces(t *testing.T) {
	now := time.Now()
	cur := UserProgress{ObjectId: 7, UserId: 42, Visited: true, IsRead: true, NumVisits: 9, TotalTime: 500}

	ch := Plan(cur, PlanOptions{SetVisits: intPtr(2)}, now)
	require.Equal(t, "set-visits", ch.Action)
	require.Equal(t, 2, ch.After.NumVisits)
	require.True(t, ch.After.Visited)
	require.Equal(t, 500, ch.After.TotalTime)

	ch = Plan(cur, PlanOptions{SetVisits: intPtr(0)}, now)
	require.Equal(t, "set-visits", ch.Action)
	require.Equal(t, 0, ch.After.NumVisits)
	require.False(t, ch.After.Visited)

	ch = Plan(cur, PlanOptions{SetVisits: intPtr(9)}, now)
	require.Equal(t, "", ch.Action)
}

func TestPlanMinVisitsOnlyRaises(t *testing.T) {
	now := time.Now()
	cur := UserProgress{ObjectId: 7, UserId: 42, Visited: true, NumVisits: 4}

	ch := Plan(cur, PlanOptions{MinVisits: 2}, now)
	require.Equal(t, "", ch.Action)

	ch = Plan(cur, PlanOptions{MinVisits: 6}, now)
	require.Equal(t, "min-visits", ch.Action)
	require.Equal(t, 6, ch.After.NumVisits)
	require.True(t, ch.After.Visited)
}

func TestPlanMarkAndVisitsCombine(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	fresh := UserProgress{ObjectId: 7, UserId: 42}

	ch := Plan(fresh, PlanOptions{MarkUnvisited: MarkCompleted, MinVisits: 3}, now)
	require.Equal(t, "mark-completed+min-visits", ch.Action)
	require.True(t, ch.After.Completed)
	require.Equal(t, 3, ch.After.NumVisits)

	ch = Plan(fresh, PlanOptions{MarkUnvisited: MarkVisited, SetVisits: intPtr(0)}, now)
	require.Equal(t, "mark-visited+set-visits", ch.Action)
	require.Equal(t, 0, ch.After.NumVisits)
	require.False(t, ch.After.Visited)
}

func TestPlanPreservesExistingCompletion(t *testing.T) {
	now := time.Now()
	cur := UserProgress{ObjectId: 7, UserId: 42, Visited: true, IsRead: true, NumVisits: 1,
		Completed: true, CompletedDate: ptr("2026-09-02T01:28:16.260Z"), TotalTime: 13888}

	ch := Plan(cur, PlanOptions{SetVisits: intPtr(2)}, now)
	require.True(t, ch.After.Completed)
	require.Equal(t, "2026-09-02T01:28:16.260Z", *ch.After.CompletedDate)
	require.Equal(t, 13888, ch.After.TotalTime)
}

func TestPlanDoneAndUndo(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	fresh := UserProgress{ObjectId: 7, UserId: 42}

	ch := PlanDone(fresh, "", false, now)
	require.Equal(t, "mark-done", ch.Action)
	require.True(t, ch.After.Completed)
	require.Equal(t, "2026-10-30T12:00:00.000Z", *ch.After.CompletedDate)
	require.Equal(t, 1, ch.After.NumVisits)

	ch = PlanDone(fresh, "2026-10-01T08:00:00.000Z", false, now)
	require.Equal(t, "2026-10-01T08:00:00.000Z", *ch.After.CompletedDate)

	done := UserProgress{ObjectId: 7, UserId: 42, Visited: true, IsRead: true, NumVisits: 2,
		Completed: true, CompletedDate: ptr("2026-09-02T01:28:16.260Z")}
	ch = PlanDone(done, "", false, now)
	require.Equal(t, "", ch.Action)

	ch = PlanDone(done, "", true, now)
	require.Equal(t, "undo-done", ch.Action)
	require.False(t, ch.After.Completed)
	require.Nil(t, ch.After.CompletedDate)
	require.Equal(t, 2, ch.After.NumVisits)

	ch = PlanDone(fresh, "", true, now)
	require.Equal(t, "", ch.Action)
}

func TestPlanViewed(t *testing.T) {
	cur := UserProgress{ObjectId: 7, UserId: 42, Visited: true, IsRead: true, NumVisits: 2, TotalTime: 30}

	ch := PlanViewed(cur, nil, 0, 120)
	require.Equal(t, "add-time", ch.Action)
	require.Equal(t, 150, ch.After.TotalTime)
	require.Equal(t, 2, ch.After.NumVisits)

	ch = PlanViewed(cur, intPtr(5), 0, 0)
	require.Equal(t, "set-visits", ch.Action)
	require.Equal(t, 5, ch.After.NumVisits)

	ch = PlanViewed(cur, intPtr(1), 0, 60)
	require.Equal(t, "set-visits+add-time", ch.Action)
	require.Equal(t, 1, ch.After.NumVisits)
	require.Equal(t, 90, ch.After.TotalTime)

	ch = PlanViewed(cur, nil, 0, 0)
	require.Equal(t, "", ch.Action)
}

func TestEqualIgnoresPointerIdentity(t *testing.T) {
	a := UserProgress{ObjectId: 7, CompletedDate: ptr("2026-09-02T01:28:16.260Z")}
	b := UserProgress{ObjectId: 7, CompletedDate: ptr("2026-09-02T01:28:16.260Z")}
	require.True(t, Equal(a, b))
	b.CompletedDate = ptr("2026-09-03T01:28:16.260Z")
	require.False(t, Equal(a, b))
}

func TestIsUnvisited(t *testing.T) {
	require.True(t, IsUnvisited(UserProgress{}))
	require.True(t, IsUnvisited(UserProgress{Visited: true, NumVisits: 0}))
	require.False(t, IsUnvisited(UserProgress{Visited: true, NumVisits: 1}))
}
