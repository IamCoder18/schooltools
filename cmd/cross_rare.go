// Rare cross-course / user-scoped surfaces from plan §5.11. These commands
// are flat top-level (no `me` parent) and take optional `--org CSV` filters.
package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
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
	EventType            int         `json:"EventType"`
	LocationName         string      `json:"LocationName"`
	OrgUnitName          string      `json:"OrgUnitName"`
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No calendar events in this window.")
		return nil
	}
	fmt.Printf("%d calendar event%s\n", len(items), plural(len(items)))
	for _, e := range items {
		when := e.StartDateTime
		if len(when) > 16 {
			when = when[:16]
		}
		fmt.Printf("  %s\torg=%d\ttype=%d\t%s\t%s\n", e.CalendarEventId.String(), e.OrgUnitId, e.EventType, when, truncate(e.Title, 60))
	}
	return nil
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
	Id              string `json:"Id"`
	Name            string `json:"Name"`
	ToolId          int    `json:"ToolId"`
	ToolName        string `json:"ToolName"`
	OrgUnitId       int    `json:"OrgUnitId"`
	OrgUnitName     string `json:"OrgUnitName"`
	StartDate       string `json:"StartDate"`
	EndDate         string `json:"EndDate"`
	DueDate         string `json:"DueDate"`
	IsCompleted     bool   `json:"IsCompleted"`
	IsOverdue       bool   `json:"IsOverdue"`
	CompletionType  int    `json:"CompletionType"`
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No due items in this window.")
		return nil
	}
	fmt.Printf("%d due item%s\n", len(items), plural(len(items)))
	for _, d := range items {
		when := d.DueDate
		if len(when) > 16 {
			when = when[:16]
		}
		org := d.OrgUnitName
		if org == "" {
			org = strconv.Itoa(d.OrgUnitId)
		}
		fmt.Printf("  %s\t%s\t%s\t%s\n", d.Id, org, when, truncate(d.Name, 60))
	}
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No overdue items.")
		return nil
	}
	fmt.Printf("%d overdue item%s\n", len(items), plural(len(items)))
	for _, d := range items {
		when := d.DueDate
		if len(when) > 16 {
			when = when[:16]
		}
		org := d.OrgUnitName
		if org == "" {
			org = strconv.Itoa(d.OrgUnitId)
		}
		fmt.Printf("  %s\t%s\t%s\t%s\n", d.Id, org, when, truncate(d.Name, 60))
	}
	return nil
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
	OrgUnitId               json.Number `json:"OrgUnitId"`
	UserId                  json.Number `json:"UserId"`
	UnreadDiscussions       int         `json:"UnreadDiscussions"`
	UnapprovedDiscussions   int         `json:"UnapprovedDiscussions"`
	UnreadAssignmentFeedback int        `json:"UnreadAssignmentFeedback"`
	UnattemptedQuizzes      int         `json:"UnattemptedQuizzes"`
	UnreadAssignmentSubmissions int      `json:"UnreadAssignmentSubmissions"`
	UngradedQuizzes         int         `json:"UngradedQuizzes"`
	PendingEnrollmentRequests int       `json:"PendingEnrollmentRequests"`
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No per-course update counts.")
		return nil
	}
	fmt.Printf("%d course%s with update counts\n", len(items), plural(len(items)))
	for _, u := range items {
		total := u.UnreadDiscussions + u.UnapprovedDiscussions +
			u.UnreadAssignmentFeedback + u.UnattemptedQuizzes +
			u.UnreadAssignmentSubmissions + u.UngradedQuizzes
		fmt.Printf("  %s\ttotal=%d\tquiz=%d unsubmitted, %d ungraded\tfeedback=%d unread\n",
			u.OrgUnitId.String(), total, u.UnattemptedQuizzes, u.UngradedQuizzes, u.UnreadAssignmentFeedback)
	}
	return nil
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No activity feed entries in this window.")
		return nil
	}
	fmt.Printf("%d feed item%s\n", len(items), plural(len(items)))
	for _, e := range items {
		title, _ := e["Title"].(string)
		ts, _ := e["Timestamp"].(string)
		if len(ts) > 16 {
			ts = ts[:16]
		}
		fmt.Printf("  %s\t%s\n", ts, truncate(title, 80))
	}
	return nil
}

func feedSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     feedEnvFile,
		AutoRefresh: feedAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}
