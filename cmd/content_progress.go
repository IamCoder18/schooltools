package cmd

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/dupdata"
	"github.com/IamCoder18/schooltools/internal/progress"
	"github.com/IamCoder18/schooltools/internal/render"
	"github.com/IamCoder18/schooltools/internal/session"
)

var (
	contentDoneDate      string
	contentDoneUndo      bool
	contentDoneJSON      bool
	contentViewSetVisits int
	contentViewMinVisits int
	contentViewTime      time.Duration
	contentViewWait      bool
	contentViewJSON      bool
	contentStatJSON      bool
)

var contentDoneCmd = &cobra.Command{
	Use:   "done <topicId>",
	Short: "Mark a content topic as done for your own progress record",
	Long: "Write the topic's userprogress record with Completed set. This is the\n" +
		"same state the UI's \"Mark as done\" produces, and it is exactly what\n" +
		"teacher-facing reports (Content Statistics, Class Progress) read — the\n" +
		"CLI never touches it unless you run this subcommand.\n\n" +
		"The topic is treated as opened once (visited, read, one visit).\n" +
		"--date backdates the completion timestamp (ISO8601). --undo clears\n" +
		"Completed and the completion date and leaves visits and time alone.\n\n" +
		"Examples:\n" +
		"  schooltools content done 19448665 --course 1522695\n" +
		"  schooltools content done 19448665 --course 1522695 --date 2026-10-01T12:00:00Z\n" +
		"  schooltools content done 19448665 --course 1522695 --undo",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		date, err := normalizeDoneDate(contentDoneDate)
		if err != nil {
			return err
		}
		if contentDoneUndo && contentDoneDate != "" {
			return fmt.Errorf("--date and --undo are mutually exclusive")
		}
		courseID, err := requireContentCourse()
		if err != nil {
			return err
		}
		topicID, err := parseTopicID(args[0])
		if err != nil {
			return err
		}
		return runContentDone(courseID, topicID, date, contentDoneUndo)
	},
}

var contentViewedCmd = &cobra.Command{
	Use:   "viewed <topicId>",
	Short: "Set a topic's visit count and/or record viewing time",
	Long: "Write the topic's userprogress visit/time fields. Nothing is changed\n" +
		"unless you pass one of the flags below, and only the fields you ask for\n" +
		"are touched — completion state, existing dates, and accumulated time\n" +
		"stay as they are.\n\n" +
		"--set-visits N   set the visit count to exactly N; topics not at N are\n" +
		"                 changed, including topics that are above N (reducing).\n" +
		"--min-visits N   give every topic below N exactly N visits (raises only).\n" +
		"--time 15m       add viewing time. The server only credits up to the\n" +
		"                 gap since the record's last visit, so the request may\n" +
		"                 come back smaller; the effective value is always\n" +
		"                 reported. --wait sleeps out the gap and re-posts until\n" +
		"                 the full amount is credited.\n\n" +
		"These values are what teachers see in Content Statistics and Class\n" +
		"Progress. The CLI never writes them unless you run this subcommand.\n\n" +
		"Examples:\n" +
		"  schooltools content viewed 19448665 --course 1522695 --min-visits 3\n" +
		"  schooltools content viewed 19448665 --course 1522695 --set-visits 1\n" +
		"  schooltools content viewed 19448665 --course 1522695 --time 15m --wait",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := validateViewedFlags(contentViewSetVisits, contentViewMinVisits, contentViewTime, contentViewWait); err != nil {
			return err
		}
		courseID, err := requireContentCourse()
		if err != nil {
			return err
		}
		topicID, err := parseTopicID(args[0])
		if err != nil {
			return err
		}
		return runContentViewed(courseID, topicID)
	},
}

var contentStatCmd = &cobra.Command{
	Use:   "progress <topicId>",
	Short: "Show your current userprogress record for a topic (read-only)",
	Long: "Read the per-topic userprogress record: completion, visits, total\n" +
		"time, and last-visited. Never-visited topics return a default-state\n" +
		"row. This command makes no writes.\n\n" +
		"Examples:\n" +
		"  schooltools content progress 19448665 --course 1522695\n" +
		"  schooltools content progress 19448665 --course 1522695 --json",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		courseID, err := requireContentCourse()
		if err != nil {
			return err
		}
		topicID, err := parseTopicID(args[0])
		if err != nil {
			return err
		}
		return runContentStat(courseID, topicID)
	},
}

func init() {
	contentDoneCmd.Flags().StringVar(&contentDoneDate, "date", "", "Completion timestamp to send (ISO8601; backdating is honored). Default: now")
	contentDoneCmd.Flags().BoolVar(&contentDoneUndo, "undo", false, "Clear Completed and the completion date instead of setting them")
	contentDoneCmd.Flags().BoolVar(&contentDoneJSON, "json", false, "Emit a JSON record {courseId, topicId, action, before, after}")

	contentViewedCmd.Flags().IntVar(&contentViewSetVisits, "set-visits", -1, "Set the visit count to exactly N (raises or lowers; -1 = leave alone)")
	contentViewedCmd.Flags().IntVar(&contentViewMinVisits, "min-visits", 0, "Raise every topic below N visits to N (leaves higher counts alone)")
	contentViewedCmd.Flags().DurationVar(&contentViewTime, "time", 0, "Record this much viewing time (e.g. 15m, 90s); clamped by the server")
	contentViewedCmd.Flags().BoolVar(&contentViewWait, "wait", false, "Sleep out the requested --time and re-post until it is fully credited")
	contentViewedCmd.Flags().BoolVar(&contentViewJSON, "json", false, "Emit a JSON record {courseId, topicId, action, before, after}")

	contentStatCmd.Flags().BoolVar(&contentStatJSON, "json", false, "Emit raw JSON instead of a report")

	contentCmd.AddCommand(contentDoneCmd, contentViewedCmd, contentStatCmd)
}

func parseTopicID(arg string) (int, error) {
	topicID, err := strconv.Atoi(arg)
	if err != nil {
		return 0, fmt.Errorf("invalid topicId %q: %w", arg, err)
	}
	return topicID, nil
}

func normalizeDoneDate(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05.000Z"), nil
		}
	}
	return "", fmt.Errorf("invalid --date %q (want ISO8601, e.g. 2026-10-01T12:00:00Z or 2026-10-01)", s)
}

func validateMarkMode(s string) error {
	switch strings.TrimSpace(s) {
	case "", progress.MarkVisited, progress.MarkCompleted:
		return nil
	default:
		return fmt.Errorf("invalid value %q (want %q or %q, or omit the flag)", s, progress.MarkVisited, progress.MarkCompleted)
	}
}

func validateViewedFlags(setVisits, minVisits int, d time.Duration, wait bool) error {
	if setVisits < -1 {
		return fmt.Errorf("--set-visits must be >= 0 (got %d)", setVisits)
	}
	if minVisits < 0 {
		return fmt.Errorf("--min-visits must be >= 0 (got %d)", minVisits)
	}
	if setVisits >= 0 && minVisits > 0 {
		return fmt.Errorf("--set-visits and --min-visits are mutually exclusive")
	}
	if d < 0 {
		return fmt.Errorf("--time must not be negative (got %s)", d)
	}
	if wait && d <= 0 {
		return fmt.Errorf("--wait requires --time")
	}
	if setVisits < 0 && minVisits <= 0 && d <= 0 {
		return fmt.Errorf("nothing to do: pass --set-visits, --min-visits, or --time")
	}
	return nil
}

func setVisitsPtr(setVisits int) *int {
	if setVisits < 0 {
		return nil
	}
	return &setVisits
}

func progressPlanFromFlags(mark string, minVisits, setVisits int) (progress.PlanOptions, bool, error) {
	mark = strings.TrimSpace(mark)
	if err := validateMarkMode(mark); err != nil {
		return progress.PlanOptions{}, false, err
	}
	if minVisits < 0 {
		return progress.PlanOptions{}, false, fmt.Errorf("--min-visits must be >= 0 (got %d)", minVisits)
	}
	if setVisits < -1 {
		return progress.PlanOptions{}, false, fmt.Errorf("--set-visits must be >= 0 (got %d)", setVisits)
	}
	if setVisits >= 0 && minVisits > 0 {
		return progress.PlanOptions{}, false, fmt.Errorf("--set-visits and --min-visits are mutually exclusive")
	}
	opts := progress.PlanOptions{
		MarkUnvisited: mark,
		MinVisits:     minVisits,
		SetVisits:     setVisitsPtr(setVisits),
	}
	enabled := opts.MarkUnvisited != "" || opts.MinVisits > 0 || opts.SetVisits != nil
	return opts, enabled, nil
}

func progressClient() (*progress.Client, error) {
	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     contentEnvFile,
		AutoRefresh: contentAutoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return nil, err
	}
	uid, err := dupdata.CurrentUserID(ens.Jar)
	if err != nil {
		return nil, err
	}
	id, err := progress.ParseUserID(uid)
	if err != nil {
		return nil, err
	}
	return progress.NewClient(ens.Jar, id), nil
}

func progressAPIError(err error) error {
	if apiErr, ok := err.(*progress.APIError); ok && apiErr.StatusCode == 401 {
		_ = session.Clear()
		return fmt.Errorf("session expired; run 'schooltools login' again")
	}
	return err
}

func runContentDone(courseID string, topicID int, date string, undo bool) error {
	client, err := progressClient()
	if err != nil {
		return err
	}
	cur, err := client.Fetch(courseID, topicID)
	if err != nil {
		return progressAPIError(err)
	}
	ch := progress.PlanDone(cur, date, undo, time.Now())
	if ch.Action == "" {
		if contentDoneJSON {
			return render.PrintJSON(doneJSON(courseID, topicID, ch))
		}
		fmt.Print(render.Title("Already in that state"))
		fmt.Print(render.KV(
			render.Row{Key: "Topic", Value: strconv.Itoa(topicID)},
			render.Row{Key: "Course", Value: courseID},
			render.Row{Key: "Completed", Value: yesNo(cur.Completed)},
		))
		fmt.Print(render.Note("no write sent"))
		return nil
	}
	echo, err := client.Update(courseID, ch.After)
	if err != nil {
		return progressAPIError(err)
	}
	ch.After = echo
	if contentDoneJSON {
		return render.PrintJSON(doneJSON(courseID, topicID, ch))
	}
	fmt.Print(render.Title("Progress updated"))
	fmt.Print(changeReport(courseID, topicID, ch))
	fmt.Print(render.Hint("schooltools content progress %d --course %s", topicID, courseID))
	return nil
}

func runContentViewed(courseID string, topicID int) error {
	client, err := progressClient()
	if err != nil {
		return err
	}
	cur, err := client.Fetch(courseID, topicID)
	if err != nil {
		return progressAPIError(err)
	}
	wantTime := int(contentViewTime.Seconds())
	ch := progress.PlanViewed(cur, setVisitsPtr(contentViewSetVisits), contentViewMinVisits, 0)
	if ch.Action == "" && wantTime == 0 {
		if contentViewJSON {
			return render.PrintJSON(viewedJSON(courseID, topicID, ch, nil))
		}
		fmt.Print(render.Title("Already in that state"))
		fmt.Print(render.KV(
			render.Row{Key: "Topic", Value: strconv.Itoa(topicID)},
			render.Row{Key: "Course", Value: courseID},
			render.Row{Key: "Visits", Value: strconv.Itoa(cur.NumVisits)},
			render.Row{Key: "Time", Value: humanSeconds(cur.TotalTime)},
		))
		fmt.Print(render.Note("no write sent"))
		return nil
	}

	var credit *progress.CreditResult
	if wantTime > 0 {
		res, err := client.CreditTime(courseID, ch.After, wantTime, contentViewWait, func(d time.Duration) {
			fmt.Print(render.Note("sleeping %s so the next write can credit the gap…", d.Round(time.Second)))
		})
		if err != nil {
			return progressAPIError(err)
		}
		credit = &res
		ch.After = res.After
		if ch.Action == "" {
			ch.Action = "add-time"
		} else {
			ch.Action += "+add-time"
		}
	} else {
		echo, err := client.Update(courseID, ch.After)
		if err != nil {
			return progressAPIError(err)
		}
		ch.After = echo
	}

	if contentViewJSON {
		return render.PrintJSON(viewedJSON(courseID, topicID, ch, credit))
	}
	fmt.Print(render.Title("Progress updated"))
	fmt.Print(changeReport(courseID, topicID, ch))
	if credit != nil {
		fmt.Print(render.KV(
			render.Row{Key: "Time requested", Value: humanSeconds(credit.Requested)},
			render.Row{Key: "Time credited", Value: humanSeconds(credit.Credited)},
		))
		if credit.Credited < credit.Requested {
			fmt.Print(render.Warn("the server only credits the gap since the last visit"))
			if !contentViewWait {
				fmt.Print(render.Hint("re-run with --wait to sleep out the remainder"))
			}
		}
	}
	fmt.Print(render.Hint("schooltools content progress %d --course %s", topicID, courseID))
	return nil
}

func runContentStat(courseID string, topicID int) error {
	client, err := progressClient()
	if err != nil {
		return err
	}
	cur, err := client.Fetch(courseID, topicID)
	if err != nil {
		return progressAPIError(err)
	}
	if contentStatJSON {
		return render.PrintJSON(map[string]any{
			"courseId": courseID,
			"topicId":  topicID,
			"progress": cur,
		})
	}
	fmt.Print(render.Title("Content progress"))
	fmt.Print(render.KV(progressRows(courseID, topicID, cur)...))
	return nil
}

func doneJSON(courseID string, topicID int, ch progress.Change) map[string]any {
	return map[string]any{
		"courseId": courseID,
		"topicId":  topicID,
		"action":   ch.Action,
		"before":   ch.Before,
		"after":    ch.After,
	}
}

func viewedJSON(courseID string, topicID int, ch progress.Change, credit *progress.CreditResult) map[string]any {
	out := doneJSON(courseID, topicID, ch)
	if credit != nil {
		out["timeRequested"] = credit.Requested
		out["timeCredited"] = credit.Credited
	}
	return out
}

func changeReport(courseID string, topicID int, ch progress.Change) string {
	rows := []render.Row{
		{Key: "Topic", Value: strconv.Itoa(topicID)},
		{Key: "Course", Value: courseID},
		{Key: "Action", Value: ch.Action},
	}
	rows = append(rows, diffRows(ch.Before, ch.After)...)
	return render.KV(rows...)
}

func progressRows(courseID string, topicID int, p progress.UserProgress) []render.Row {
	return []render.Row{
		{Key: "Topic", Value: strconv.Itoa(topicID)},
		{Key: "Course", Value: courseID},
		{Key: "Completed", Value: yesNo(p.Completed)},
		{Key: "Completed at", Value: timeOrDash(p.CompletedDate)},
		{Key: "Visited", Value: yesNo(p.Visited)},
		{Key: "Read", Value: yesNo(p.IsRead)},
		{Key: "Visits", Value: strconv.Itoa(p.NumVisits)},
		{Key: "Time", Value: humanSeconds(p.TotalTime)},
		{Key: "Last visited", Value: timeOrDash(p.LastVisited)},
		{Key: "Exempt", Value: yesNo(p.IsExempt)},
	}
}

func diffRows(before, after progress.UserProgress) []render.Row {
	rows := []render.Row{
		{Key: "Completed", Value: diffWord(yesNo(before.Completed), yesNo(after.Completed))},
		{Key: "Completed at", Value: diffWord(timeOrDash(before.CompletedDate), timeOrDash(after.CompletedDate))},
		{Key: "Visited", Value: diffWord(yesNo(before.Visited), yesNo(after.Visited))},
		{Key: "Read", Value: diffWord(yesNo(before.IsRead), yesNo(after.IsRead))},
		{Key: "Visits", Value: diffWord(strconv.Itoa(before.NumVisits), strconv.Itoa(after.NumVisits))},
		{Key: "Time", Value: diffWord(humanSeconds(before.TotalTime), humanSeconds(after.TotalTime))},
	}
	return rows
}

func diffWord(a, b string) string {
	if a == b {
		return b
	}
	return a + " → " + b
}

func timeOrDash(s *string) string {
	if s == nil {
		return "—"
	}
	return render.DateTime(*s)
}

func humanSeconds(n int) string {
	if n < 0 {
		n = 0
	}
	h := n / 3600
	m := (n % 3600) / 60
	s := n % 60
	switch {
	case h > 0:
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm %ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}
