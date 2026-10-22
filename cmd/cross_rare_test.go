package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/IamCoder18/schooltools/internal/render"
)

func withUTCLocal(t *testing.T) {
	t.Helper()
	prev := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prev })
}

func localClock(iso string) string {
	t, ok := render.Parse(iso)
	if !ok {
		return "—"
	}
	return t.Local().Format("3:04 PM")
}

func localStamp(iso string) string {
	t, ok := render.Parse(iso)
	if !ok {
		return "—"
	}
	return t.Local().Format("Mon, Jan 2 2006, 3:04 PM")
}

func TestCalDayHeading(t *testing.T) {
	withUTCLocal(t)
	now := time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)
	cases := []struct {
		key  string
		want string
	}{
		{"2026-10-26", "Mon, Oct 26  ·  in 5 days"},
		{"2026-10-21", "Wed, Oct 21  ·  today"},
		{"2026-10-22", "Thu, Oct 22  ·  tomorrow"},
		{"2026-10-20", "Tue, Oct 20  ·  yesterday"},
		{"2026-10-14", "Wed, Oct 14  ·  7 days ago"},
		{"nonsense", "Undated"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, calDayHeading(tc.key, now), tc.key)
	}
}

func TestCalEventTime(t *testing.T) {
	withUTCLocal(t)
	allDay := CalendarEvent{IsAllDayEvent: true, StartDay: "2026-10-26", EndDay: "2026-10-26"}
	require.Equal(t, "all day", calEventTime(allDay))

	timed := CalendarEvent{StartDateTime: "2026-10-26T09:00:00.000Z", EndDateTime: "2026-10-26T11:00:00.000Z"}
	require.Equal(t, localClock("2026-10-26T09:00:00.000Z")+" – "+localClock("2026-10-26T11:00:00.000Z"), calEventTime(timed))

	cross := CalendarEvent{StartDateTime: "2026-10-26T22:00:00.000Z", EndDateTime: "2026-10-27T02:00:00.000Z"}
	require.Equal(t,
		localStamp("2026-10-26T22:00:00.000Z")+" – "+localStamp("2026-10-27T02:00:00.000Z"),
		calEventTime(cross))

	noEnd := CalendarEvent{StartDateTime: "2026-10-26T09:00:00.000Z"}
	require.Equal(t, localClock("2026-10-26T09:00:00.000Z"), calEventTime(noEnd))

	bad := CalendarEvent{StartDateTime: "not a time"}
	require.Equal(t, "—", calEventTime(bad))
}

func TestCalGroupDays(t *testing.T) {
	withUTCLocal(t)
	now := time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)
	items := []CalendarEvent{
		{Title: "Late", StartDateTime: "2026-10-27T09:00:00.000Z"},
		{Title: "All day", IsAllDayEvent: true, StartDay: "2026-10-26", EndDay: "2026-10-26"},
		{Title: "Afternoon", StartDateTime: "2026-10-26T14:00:00.000Z"},
	}
	days := calGroupDays(items, now)
	require.Len(t, days, 2)
	require.Equal(t, "Mon, Oct 26  ·  in 5 days", days[0].Heading)
	require.Len(t, days[0].Events, 2)
	require.Equal(t, "All day", days[0].Events[0].Title)
	require.Equal(t, "Afternoon", days[0].Events[1].Title)
	require.Equal(t, "Tue, Oct 27  ·  in 6 days", days[1].Heading)
	require.Equal(t, "Late", days[1].Events[0].Title)
}

func TestCalRender(t *testing.T) {
	withUTCLocal(t)
	now := time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)
	items := []CalendarEvent{
		{
			Title:         "Field trip deposit due",
			OrgUnitName:   "Biology 20",
			IsAllDayEvent: true,
			StartDay:      "2026-10-26",
			EndDay:        "2026-10-26",
			EventType:     5,
		},
		{
			Title:         "Unit 4 test",
			OrgUnitName:   "Math 30-1",
			StartDateTime: "2026-10-26T09:00:00.000Z",
			EndDateTime:   "2026-10-26T11:00:00.000Z",
			EventType:     2,
			LocationName:  "Room 204",
		},
	}
	out := calRender(items, now)
	require.Contains(t, out, "Calendar")
	require.Contains(t, out, "Mon, Oct 26")
	require.Contains(t, out, "Field trip deposit due")
	require.Contains(t, out, "all day")
	require.Contains(t, out, "Biology 20")
	require.Contains(t, out, "Unit 4 test")
	require.Contains(t, out, "9:00 AM – 11:00 AM")
	require.Contains(t, out, "Room 204")
}

func TestDueCell(t *testing.T) {
	withUTCLocal(t)
	got := dueCell(DueItem{DueDate: "2026-10-24T16:00:00.000Z"})
	require.True(t, strings.HasSuffix(got, "  ·  Sat, Oct 24 2026, 4:00 PM"), got)
}

func TestDueItemsTable(t *testing.T) {
	withUTCLocal(t)
	items := []DueItem{{Name: "Essay", OrgUnitName: "English 20", DueDate: "2026-10-24T16:00:00.000Z"}}
	plain := dueItemsTable(items, false)
	require.Contains(t, plain, "Course")
	require.Contains(t, plain, "Due")
	require.Contains(t, plain, "Title")
	require.Contains(t, plain, "Essay")
	require.NotContains(t, plain, "Status")

	over := dueItemsTable(items, true)
	require.Contains(t, over, "Status")
	require.Contains(t, over, "overdue")
}

func TestDueCourseLabel(t *testing.T) {
	require.Equal(t, "English 20", dueCourseLabel(DueItem{OrgUnitName: "English 20", OrgUnitId: 7}))
	require.Equal(t, "7", dueCourseLabel(DueItem{OrgUnitId: 7}))
}

func TestUpdateCounts(t *testing.T) {
	u := Update{
		UnreadDiscussions:           2,
		UnapprovedDiscussions:       1,
		UnreadAssignmentFeedback:    3,
		UnattemptedQuizzes:          4,
		UnreadAssignmentSubmissions: 5,
		UngradedQuizzes:             6,
		PendingEnrollmentRequests:   7,
	}
	c := updateCounts(u)
	require.Equal(t, 3, c.Discussions)
	require.Equal(t, 3, c.Feedback)
	require.Equal(t, 10, c.Quizzes)
	require.Equal(t, 5, c.Submissions)
	require.Equal(t, 7, c.Requests)
	require.Equal(t, 28, c.total())
}

func TestUpdateTotalsLine(t *testing.T) {
	items := []Update{
		{UnreadDiscussions: 1, UnattemptedQuizzes: 2},
		{UnreadAssignmentFeedback: 3},
		{},
	}
	got := updateTotalsLine(sumUpdates(items), 2)
	require.Equal(t, "6 updates across 2 courses  ·  discussions 1  ·  feedback 3  ·  quizzes 2  ·  submissions 0  ·  requests 0", got)
}

func TestFeedFields(t *testing.T) {
	e := map[string]any{
		"Title":       "  New grade posted  ",
		"Timestamp":   "2026-10-24T16:00:00.000Z",
		"ToolName":    "Discussions",
		"OrgUnitName": "English 20",
	}
	require.Equal(t, "New grade posted", feedField(e, "Title", "Name"))
	require.Equal(t, "2026-10-24T16:00:00.000Z", feedField(e, "Timestamp"))
	require.Equal(t, "Discussions  ·  English 20", feedSource(e))

	fallback := map[string]any{"Type": "news", "Name": "Hello"}
	require.Equal(t, "Hello", feedField(fallback, "Title", "Name"))
	require.Equal(t, "news", feedSource(fallback))

	require.Equal(t, "—", feedSource(map[string]any{}))
}
