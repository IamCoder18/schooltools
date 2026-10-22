// Rare cross-course / user-scoped surfaces from plan §5.11. These commands
// are flat top-level (no `me` parent) and take optional `--org CSV` filters.
package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/dupdata"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
	"github.com/IamCoder18/schooltools/internal/table"
	"github.com/IamCoder18/schooltools/internal/ua"
)

// ───────────────────────────── calendar ─────────────────────────────

var (
	calEnvFile string
	calAutoRef bool
	calJSON    bool
	calFrom    string
	calTo      string
	calEvent   int
	calOrg     string
)

type CalendarEvent struct {
	CalendarEventId      json.Number `json:"CalendarEventId"`
	OrgUnitId            int         `json:"OrgUnitId"`
	CreatorUserId        int         `json:"CreatorUserId"`
	Title                string      `json:"Title"`
	Description          string      `json:"Description"`
	CalendarEventViewUrl string      `json:"CalendarEventViewUrl"`
	IsAllDayEvent        bool        `json:"IsAllDayEvent"`
	StartDateTime        string      `json:"StartDateTime"`
	EndDateTime          string      `json:"EndDateTime"`
	StartDay             string      `json:"StartDay"`
	EndDay               string      `json:"EndDay"`
	EventType            int         `json:"EventType"`
	LocationName         string      `json:"LocationName"`
	OrgUnitName          string      `json:"OrgUnitName"`
	OrgUnitCode          string      `json:"OrgUnitCode"`
}

func init() {
	root := &cobra.Command{
		Use:   "calendar",
		Short: "My aggregated calendar events (rare)",
		Long: "My calendar events across all courses. The D2L API requires a\n" +
			"date window (per research §3.4); pass `--from`/`--to` or use the\n" +
			"default 30-day window.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runCalendar() },
	}
	root.Flags().StringVar(&calFrom, "from", "", "ISO 8601 lower bound (e.g. 2026-09-01 or 2026-09-01T00:00:00Z)")
	root.Flags().StringVar(&calTo, "to", "", "ISO 8601 upper bound")
	root.Flags().StringVar(&calOrg, "org", "", "CSV of OrgUnitIds to restrict to")
	root.Flags().IntVar(&calEvent, "event-type", 0, "D2L event-type enum (1=Assignment, 2=Quiz, 3=Discussion, 4=Module, 5=Custom)")
	root.Flags().StringVarP(&calEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&calAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&calJSON, "json", false, "Emit raw JSON")
	rootCmd.AddCommand(root)
}

func runCalendar() error {
	ens, err := calSession()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	from := calFrom
	to := calTo
	if from == "" && to == "" {
		from = now.AddDate(0, 0, -30).Format("2006-01-02T15:04:05.000Z")
		to = now.AddDate(0, 0, 30).Format("2006-01-02T15:04:05.000Z")
	} else {
		tr, err := dupdata.ParseTimeRange(from, to, "", now)
		if err != nil {
			return err
		}
		if tr.From.IsZero() {
			from = now.AddDate(0, 0, -30).Format("2006-01-02T15:04:05.000Z")
		} else {
			from = tr.From.UTC().Format("2006-01-02T15:04:05.000Z")
		}
		if tr.To.IsZero() {
			to = now.AddDate(0, 0, 30).Format("2006-01-02T15:04:05.000Z")
		} else {
			to = tr.To.UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	// orgUnitIdsCSV is required. If not supplied, default to all enrolled
	// courses — fetched via the manageCourses API.
	orgs := calOrg
	if orgs == "" {
		csv, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		orgs = csv
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/calendar/events/myEvents/?startDateTime=%s&endDateTime=%s&orgUnitIdsCSV=%s",
		ua.D2LBase, dupdata.D2LVersion(), from, to, orgs)
	if calEvent > 0 {
		raw += fmt.Sprintf("&eventType=%d", calEvent)
	}
	items, err := dupdata.FetchJSONList[CalendarEvent](raw, ens.Jar)
	if err != nil {
		return err
	}
	if calJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No calendar events in this window."))
		return nil
	}
	fmt.Print(calRender(items, time.Now()))
	return nil
}

type calDay struct {
	Heading string
	Events  []CalendarEvent
}

func calEventStart(e CalendarEvent) string {
	if e.IsAllDayEvent && e.StartDay != "" {
		return e.StartDay
	}
	return e.StartDateTime
}

func calCourseLabel(e CalendarEvent) string {
	switch {
	case e.OrgUnitName != "":
		return e.OrgUnitName
	case e.OrgUnitCode != "":
		return e.OrgUnitCode
	default:
		return fmt.Sprintf("org %d", e.OrgUnitId)
	}
}

func calEventTypeLabel(t int) string {
	switch t {
	case 1:
		return "assignment"
	case 2:
		return "quiz"
	case 3:
		return "discussion"
	case 4:
		return "module"
	case 5:
		return "custom"
	default:
		return ""
	}
}

func calEventTime(e CalendarEvent) string {
	if e.IsAllDayEvent {
		return "all day"
	}
	start, hasStart := render.Parse(e.StartDateTime)
	end, hasEnd := render.Parse(e.EndDateTime)
	switch {
	case !hasStart:
		return "—"
	case hasEnd && start.Local().Format("2006-01-02") != end.Local().Format("2006-01-02"):
		return render.DateTime(e.StartDateTime) + " – " + render.DateTime(e.EndDateTime)
	case hasEnd:
		return render.Time(e.StartDateTime) + " – " + render.Time(e.EndDateTime)
	default:
		return render.Time(e.StartDateTime)
	}
}

func calDayHeading(key string, now time.Time) string {
	day, ok := render.Parse(key)
	if !ok {
		return "Undated"
	}
	label := day.Format("Mon, Jan 2")
	ny, nm, nd := now.Date()
	dy, dm, dd := day.Date()
	base := time.Date(ny, nm, nd, 0, 0, 0, 0, time.UTC)
	target := time.Date(dy, dm, dd, 0, 0, 0, 0, time.UTC)
	switch n := int(target.Sub(base).Hours() / 24); {
	case n == 0:
		return label + "  ·  today"
	case n == 1:
		return label + "  ·  tomorrow"
	case n == -1:
		return label + "  ·  yesterday"
	case n > 0:
		return label + "  ·  in " + render.Count(n, "day")
	default:
		return label + "  ·  " + render.Count(-n, "day") + " ago"
	}
}

func calDayKey(e CalendarEvent) string {
	s := strings.TrimSpace(calEventStart(e))
	t, ok := render.Parse(s)
	if !ok {
		return ""
	}
	if len(s) <= 10 {
		return t.Format("2006-01-02")
	}
	return t.Local().Format("2006-01-02")
}

func calGroupDays(items []CalendarEvent, now time.Time) []calDay {
	sorted := append([]CalendarEvent(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		si, sj := calEventStart(sorted[i]), calEventStart(sorted[j])
		ti, oi := render.Parse(si)
		tj, oj := render.Parse(sj)
		if oi && oj && !ti.Equal(tj) {
			return ti.Before(tj)
		}
		return si < sj
	})
	var days []calDay
	byKey := map[string]int{}
	for _, e := range sorted {
		key := calDayKey(e)
		if key == "" {
			key = "Undated"
		}
		if i, seen := byKey[key]; seen {
			days[i].Events = append(days[i].Events, e)
			continue
		}
		byKey[key] = len(days)
		days = append(days, calDay{Heading: calDayHeading(key, now), Events: []CalendarEvent{e}})
	}
	return days
}

func calEventBlock(e CalendarEvent) string {
	w := render.Width() - 4
	if w < 24 {
		w = 24
	}
	var b strings.Builder
	b.WriteString("  " + render.Truncate(strings.TrimSpace(e.Title), w) + "\n")
	meta := calCourseLabel(e) + "  ·  " + calEventTime(e)
	if t := calEventTypeLabel(e.EventType); t != "" {
		meta += "  ·  " + t
	}
	if loc := strings.TrimSpace(e.LocationName); loc != "" {
		meta += "  ·  " + loc
	}
	b.WriteString("    " + render.Muted(render.Truncate(meta, w)) + "\n")
	if d := strings.TrimSpace(htmlToText(e.Description)); d != "" {
		for _, line := range strings.Split(render.Wrap(d, w-2), "\n") {
			b.WriteString("    " + render.Muted(line) + "\n")
		}
	}
	return b.String()
}

func calRender(items []CalendarEvent, now time.Time) string {
	var b strings.Builder
	b.WriteString(render.Title("Calendar"))
	for _, d := range calGroupDays(items, now) {
		b.WriteString(render.Section(d.Heading))
		for _, e := range d.Events {
			b.WriteString(calEventBlock(e))
		}
	}
	return b.String()
}

func calSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     calEnvFile,
		AutoRefresh: calAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

// ───────────────────────────── due / overdue ─────────────────────────────

var (
	dueEnvFile string
	dueAutoRef bool
	dueJSON    bool
	dueOrg     string
	dueWindow  string
	dueFrom    string
	dueTo      string
)

type DueItem struct {
	Id             string `json:"Id"`
	Name           string `json:"Name"`
	ToolId         int    `json:"ToolId"`
	ToolName       string `json:"ToolName"`
	OrgUnitId      int    `json:"OrgUnitId"`
	OrgUnitName    string `json:"OrgUnitName"`
	StartDate      string `json:"StartDate"`
	EndDate        string `json:"EndDate"`
	DueDate        string `json:"DueDate"`
	IsCompleted    bool   `json:"IsCompleted"`
	IsOverdue      bool   `json:"IsOverdue"`
	CompletionType int    `json:"CompletionType"`
}

func init() {
	dueCmd := &cobra.Command{
		Use:   "due",
		Short: "Items with upcoming due dates (rare)",
		Long: "Items with upcoming due dates across all (or filtered) courses.\n" +
			"Pass `--window 7d` for a relative range, or `--from`/`--to` for an\n" +
			"absolute range. The D2L API requires a date window.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runDue() },
	}
	dueCmd.Flags().StringVar(&dueOrg, "org", "", "CSV of OrgUnitIds to restrict to")
	dueCmd.Flags().StringVar(&dueWindow, "window", "7d", "Relative window (e.g. 1d, 7d, 30d, 12h)")
	dueCmd.Flags().StringVar(&dueFrom, "from", "", "ISO 8601 lower bound (overrides --window)")
	dueCmd.Flags().StringVar(&dueTo, "to", "", "ISO 8601 upper bound (overrides --window)")
	dueCmd.Flags().StringVarP(&dueEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	dueCmd.Flags().BoolVar(&dueAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	dueCmd.Flags().BoolVar(&dueJSON, "json", false, "Emit raw JSON")
	rootCmd.AddCommand(dueCmd)

	overCmd := &cobra.Command{
		Use:   "overdue",
		Short: "Items past their due date (rare)",
		Long:  "Cross-course overdue items. The D2L web inbox is faster for most users; this is for scripting.",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runOverdue() },
	}
	overCmd.Flags().StringVar(&dueOrg, "org", "", "CSV of OrgUnitIds to restrict to")
	overCmd.Flags().StringVarP(&dueEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	overCmd.Flags().BoolVar(&dueAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	overCmd.Flags().BoolVar(&dueJSON, "json", false, "Emit raw JSON")
	rootCmd.AddCommand(overCmd)
}

func runDue() error {
	ens, err := dueSession()
	if err != nil {
		return err
	}
	tr, err := dupdata.ParseTimeRange(dueFrom, dueTo, dueWindow, time.Now().UTC())
	if err != nil {
		return err
	}
	orgs := dueOrg
	if orgs == "" {
		csv, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		orgs = csv
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/content/myItems/due/?startDateTime=%s&endDateTime=%s&orgUnitIdsCSV=%s",
		ua.D2LBase, dupdata.D2LVersion(),
		tr.From.UTC().Format("2006-01-02T15:04:05.000Z"),
		tr.To.UTC().Format("2006-01-02T15:04:05.000Z"),
		orgs)
	items, err := dupdata.FetchJSONList[DueItem](raw, ens.Jar)
	if err != nil {
		return err
	}
	if dueJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No due items in this window."))
		return nil
	}
	fmt.Print(render.Title("Due items"))
	fmt.Print(dueItemsTable(items, false))
	return nil
}

func runOverdue() error {
	ens, err := dueSession()
	if err != nil {
		return err
	}
	orgs := dueOrg
	if orgs == "" {
		csv, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		orgs = csv
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/overdueItems/myItems?orgUnitIdsCSV=%s",
		ua.D2LBase, dupdata.D2LVersion(), orgs)
	items, err := dupdata.FetchJSONList[DueItem](raw, ens.Jar)
	if err != nil {
		return err
	}
	if dueJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No overdue items."))
		return nil
	}
	fmt.Print(render.Title("Overdue items"))
	fmt.Print(dueItemsTable(items, true))
	return nil
}

func dueCourseLabel(d DueItem) string {
	if d.OrgUnitName != "" {
		return d.OrgUnitName
	}
	return strconv.Itoa(d.OrgUnitId)
}

func dueCell(d DueItem) string {
	return render.Countdown(d.DueDate) + "  ·  " + render.DateTime(d.DueDate)
}

func dueItemsTable(items []DueItem, overdue bool) string {
	headers := []string{"Course", "Due"}
	specs := []table.ColumnSpec{table.Fixed(24), table.Fixed(42)}
	if overdue {
		headers = append(headers, "Status")
		specs = append(specs, table.Fixed(10))
	}
	headers = append(headers, "Title")
	specs = append(specs, table.Flex(28))
	rows := make([][]string, len(items))
	for i, d := range items {
		row := []string{render.Truncate(dueCourseLabel(d), 24), dueCell(d)}
		if overdue {
			row = append(row, render.StatusWord("overdue"))
		}
		row = append(row, render.Truncate(d.Name, 52))
		rows[i] = row
	}
	return table.Render(table.Options{
		Headers: headers,
		Widths:  table.Widths(specs, table.TerminalWidth()),
		Rows:    rows,
	})
}

func dueSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     dueEnvFile,
		AutoRefresh: dueAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

// ───────────────────────────── update ─────────────────────────────

var (
	updEnvFile string
	updAutoRef bool
	updJSON    bool
	updOrg     string
)

type Update struct {
	OrgUnitId                   json.Number `json:"OrgUnitId"`
	UserId                      json.Number `json:"UserId"`
	UnreadDiscussions           int         `json:"UnreadDiscussions"`
	UnapprovedDiscussions       int         `json:"UnapprovedDiscussions"`
	UnreadAssignmentFeedback    int         `json:"UnreadAssignmentFeedback"`
	UnattemptedQuizzes          int         `json:"UnattemptedQuizzes"`
	UnreadAssignmentSubmissions int         `json:"UnreadAssignmentSubmissions"`
	UngradedQuizzes             int         `json:"UngradedQuizzes"`
	PendingEnrollmentRequests   int         `json:"PendingEnrollmentRequests"`
}

func init() {
	root := &cobra.Command{
		Use:   "update",
		Short: "Per-course unread message / unread grade counts (rare)",
		Long: "Per-course update counts. The D2L notifications inbox covers\n" +
			"this; this command is for scripting.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runUpdate() },
	}
	root.Flags().StringVar(&updOrg, "org", "", "CSV of OrgUnitIds to restrict to")
	root.Flags().StringVarP(&updEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&updAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&updJSON, "json", false, "Emit raw JSON")
	rootCmd.AddCommand(root)
}

func runUpdate() error {
	ens, err := updSession()
	if err != nil {
		return err
	}
	orgs := updOrg
	if orgs == "" {
		csv, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		orgs = csv
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/updates/myUpdates/?orgUnitIdsCSV=%s",
		ua.D2LBase, dupdata.D2LVersion(), orgs)
	items, err := dupdata.FetchJSONList[Update](raw, ens.Jar)
	if err != nil {
		return err
	}
	if updJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No per-course update counts."))
		return nil
	}
	var visible []Update
	for _, u := range items {
		if updateCounts(u).total() > 0 {
			visible = append(visible, u)
		}
	}
	if len(visible) == 0 {
		fmt.Print(render.Empty("No updates in any course."))
		return nil
	}
	fmt.Print(render.Title("Updates"))
	rows := make([][]string, len(visible))
	for i, u := range visible {
		c := updateCounts(u)
		rows[i] = []string{
			u.OrgUnitId.String(),
			strconv.Itoa(c.Discussions),
			strconv.Itoa(c.Feedback),
			strconv.Itoa(c.Quizzes),
			strconv.Itoa(c.Submissions),
			strconv.Itoa(c.Requests),
			strconv.Itoa(c.total()),
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"Course", "Discussions", "Feedback", "Quizzes", "Submissions", "Requests", "Total"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(10), table.Fixed(12), table.Fixed(10), table.Fixed(9), table.Fixed(12), table.Fixed(10), table.Fixed(8)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	if hidden := len(items) - len(visible); hidden > 0 {
		fmt.Print(render.Empty(fmt.Sprintf("%s with no updates are not shown.", render.Count(hidden, "course"))))
	}
	fmt.Print(render.KV(render.Row{Key: "Totals", Value: updateTotalsLine(sumUpdates(items), len(visible))}))
	return nil
}

type updateTotals struct {
	Discussions int
	Feedback    int
	Quizzes     int
	Submissions int
	Requests    int
}

func (t updateTotals) total() int {
	return t.Discussions + t.Feedback + t.Quizzes + t.Submissions + t.Requests
}

func updateCounts(u Update) updateTotals {
	return updateTotals{
		Discussions: u.UnreadDiscussions + u.UnapprovedDiscussions,
		Feedback:    u.UnreadAssignmentFeedback,
		Quizzes:     u.UnattemptedQuizzes + u.UngradedQuizzes,
		Submissions: u.UnreadAssignmentSubmissions,
		Requests:    u.PendingEnrollmentRequests,
	}
}

func sumUpdates(items []Update) updateTotals {
	var t updateTotals
	for _, u := range items {
		c := updateCounts(u)
		t.Discussions += c.Discussions
		t.Feedback += c.Feedback
		t.Quizzes += c.Quizzes
		t.Submissions += c.Submissions
		t.Requests += c.Requests
	}
	return t
}

func updateTotalsLine(t updateTotals, courses int) string {
	return fmt.Sprintf("%s across %s  ·  discussions %d  ·  feedback %d  ·  quizzes %d  ·  submissions %d  ·  requests %d",
		render.Count(t.total(), "update"), render.Count(courses, "course"),
		t.Discussions, t.Feedback, t.Quizzes, t.Submissions, t.Requests)
}

func updSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     updEnvFile,
		AutoRefresh: updAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

// ───────────────────────────── feed ─────────────────────────────

var (
	feedEnvFile string
	feedAutoRef bool
	feedJSON    bool
	feedSince   string
	feedUntil   string
)

func init() {
	root := &cobra.Command{
		Use:   "feed",
		Short: "Aggregated activity feed across all tools (rare)",
		Long:  "Cross-tool activity feed (D2L lp product). The D2L activity feed UI covers this; this command is for scripting.",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runFeed() },
	}
	root.Flags().StringVar(&feedSince, "since", "", "ISO 8601 lower bound")
	root.Flags().StringVar(&feedUntil, "until", "", "ISO 8601 upper bound")
	root.Flags().StringVarP(&feedEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&feedAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&feedJSON, "json", false, "Emit raw JSON")
	rootCmd.AddCommand(root)
}

func runFeed() error {
	ens, err := feedSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/lp/%s/feed/", ua.D2LBase, dupdata.LPVersion())
	if feedSince != "" {
		raw += "?since=" + strings.ReplaceAll(feedSince, " ", "+")
	}
	if feedUntil != "" {
		sep := "?"
		if strings.Contains(raw, "?") {
			sep = "&"
		}
		raw += sep + "until=" + strings.ReplaceAll(feedUntil, " ", "+")
	}
	items, err := dupdata.FetchJSONList[map[string]any](raw, ens.Jar)
	if err != nil {
		return err
	}
	if feedJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No activity feed entries in this window."))
		return nil
	}
	fmt.Print(render.Title("Activity feed"))
	rows := make([][]string, len(items))
	for i, e := range items {
		rows[i] = []string{
			render.When(feedField(e, "Timestamp", "Date", "CreatedDate", "ModifiedDate")),
			render.Truncate(feedField(e, "Title", "Name", "Subject"), 52),
			render.Truncate(feedSource(e), 26),
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"When", "Title", "Source"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(46), table.Flex(30), table.Fixed(26)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	return nil
}

func feedField(e map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := e[k].(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				return s
			}
		}
	}
	return ""
}

func feedSource(e map[string]any) string {
	var parts []string
	for _, k := range []string{"ToolName", "OrgUnitName"} {
		if s := feedField(e, k); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, "  ·  ")
	}
	if s := feedField(e, "Source", "Type", "ActivityType"); s != "" {
		return s
	}
	return "—"
}

func feedSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     feedEnvFile,
		AutoRefresh: feedAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}
