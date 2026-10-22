package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/render"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/ua"
)

var (
	quizCourse    string
	quizEnvFile   string
	quizAutoRef   bool
	quizJSON      bool
	quizNoRelated bool
)

type QuizRichText struct {
	Text        RichText `json:"Text"`
	IsDisplayed bool     `json:"IsDisplayed"`
}

func (q QuizRichText) Plain() string {
	return q.Text.Plain()
}

type QuizTimeLimit struct {
	IsEnforced     bool `json:"IsEnforced"`
	ShowClock      bool `json:"ShowClock"`
	TimeLimitValue int  `json:"TimeLimitValue"`
}

type QuizLateInfo struct {
	LateSubmissionOption int  `json:"LateSubmissionOption"`
	LateLimitMinutes     *int `json:"LateLimitMinutes"`
}

type QuizAttempts struct {
	IsUnlimited             bool `json:"IsUnlimited"`
	NumberOfAttemptsAllowed int  `json:"NumberOfAttemptsAllowed"`
}

func (a *QuizAttempts) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		return nil
	}
	if !strings.HasPrefix(s, "{") {
		var n int
		if err := json.Unmarshal(b, &n); err != nil {
			return err
		}
		a.NumberOfAttemptsAllowed = n
		return nil
	}
	type alias QuizAttempts
	var v alias
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	*a = QuizAttempts(v)
	return nil
}

func (a QuizAttempts) Label() string {
	if a.IsUnlimited {
		return "unlimited"
	}
	if a.NumberOfAttemptsAllowed <= 0 {
		return "—"
	}
	return render.Count(a.NumberOfAttemptsAllowed, "attempt") + " allowed"
}

type Quiz struct {
	QuizId                          json.Number   `json:"QuizId"`
	Name                            string        `json:"Name"`
	IsActive                        bool          `json:"IsActive"`
	SortOrder                       int           `json:"SortOrder"`
	AutoExportToGrades              bool          `json:"AutoExportToGrades"`
	GradeItemId                     *int          `json:"GradeItemId"`
	IsAutoSetGraded                 bool          `json:"IsAutoSetGraded"`
	Instructions                    QuizRichText  `json:"Instructions"`
	Description                     QuizRichText  `json:"Description"`
	Header                          QuizRichText  `json:"Header"`
	Footer                          QuizRichText  `json:"Footer"`
	StartDate                       *string       `json:"StartDate"`
	EndDate                         *string       `json:"EndDate"`
	DueDate                         *string       `json:"DueDate"`
	DisplayInCalendar               bool          `json:"DisplayInCalendar"`
	AttemptsAllowed                 QuizAttempts  `json:"AttemptsAllowed"`
	LateSubmissionInfo              QuizLateInfo  `json:"LateSubmissionInfo"`
	SubmissionTimeLimit             QuizTimeLimit `json:"SubmissionTimeLimit"`
	SubmissionGracePeriod           *int          `json:"SubmissionGracePeriod"`
	Password                        *string       `json:"Password"`
	CalcTypeId                      int           `json:"CalcTypeId"`
	CategoryId                      *int          `json:"CategoryId"`
	PreventMovingBackwards          bool          `json:"PreventMovingBackwards"`
	Shuffle                         bool          `json:"Shuffle"`
	ActivityId                      string        `json:"ActivityId"`
	AllowOnlyUsersWithSpecialAccess bool          `json:"AllowOnlyUsersWithSpecialAccess"`
	IsRetakeIncorrectOnly           bool          `json:"IsRetakeIncorrectOnly"`
	PagingTypeId                    *int          `json:"PagingTypeId"`
	IsSynchronous                   bool          `json:"IsSynchronous"`
	DeductionPercentage             *float64      `json:"DeductionPercentage"`
	HideQuestionPoints              bool          `json:"HideQuestionPoints"`
	IsSingleSession                 bool          `json:"IsSingleSession"`
	AnnotationToolsEnabled          bool          `json:"AnnotationToolsEnabled"`
	AllowHints                      bool          `json:"AllowHints"`
	DisableRightClick               bool          `json:"DisableRightClick"`
	DisablePagerAndAlerts           bool          `json:"DisablePagerAndAlerts"`
	NotificationEmail               *string       `json:"NotificationEmail"`

	TimeLimit any `json:"TimeLimit"`
}

type QuizAttempt struct {
	AttemptId                  int         `json:"AttemptId"`
	QuizId                     int         `json:"QuizId"`
	UserId                     int         `json:"UserId"`
	AttemptNumber              int         `json:"AttemptNumber"`
	Score                      json.Number `json:"Score"`
	Started                    string      `json:"Started"`
	Completed                  string      `json:"Completed"`
	AttemptFeedback            RichText    `json:"AttemptFeedback"`
	FeedbackLastModified       string      `json:"FeedbackLastModified"`
	IsPublished                bool        `json:"IsPublished"`
	IsRetakeIncorrectOnly      bool        `json:"IsRetakeIncorrectOnly"`
	AttemptDueDate             string      `json:"AttemptDueDate"`
	AttemptEnforceTimeLimit    bool        `json:"AttemptEnforceTimeLimit"`
	AttemptSubmissionTimeLimit int         `json:"AttemptSubmissionTimeLimit"`
	AttemptIsSynchronous       bool        `json:"AttemptIsSynchronous"`

	Id          int         `json:"Id"`
	StartDate   string      `json:"StartDate"`
	EndDate     string      `json:"EndDate"`
	MaxScore    json.Number `json:"MaxScore"`
	IsCompleted bool        `json:"IsCompleted"`
}

func (a QuizAttempt) ID() int {
	if a.AttemptId != 0 {
		return a.AttemptId
	}
	return a.Id
}

func (a QuizAttempt) StartTime() string {
	if a.Started != "" {
		return a.Started
	}
	return a.StartDate
}

func (a QuizAttempt) EndTime() string {
	if a.Completed != "" {
		return a.Completed
	}
	return a.EndDate
}

func (a QuizAttempt) Done() bool {
	return a.IsCompleted || a.Completed != ""
}

type quizReport struct {
	Quiz     Quiz          `json:"quiz"`
	Attempts []QuizAttempt `json:"attempts"`
	Notes    []string      `json:"notes"`
}

func init() {
	root := &cobra.Command{
		Use:   "quiz",
		Short: "List quizzes and show my attempts (per-course)",
		Long: "Browse a course's quizzes, view their metadata (dates, attempts\n" +
			"allowed, time limit, grade calculation), and list / inspect your own\n" +
			"attempts.\n\n" +
			"`quiz get` fetches the quiz and your attempts together and summarises\n" +
			"attempts used and best score. Pass --no-related to fetch the quiz alone.\n\n" +
			"Students cannot enumerate quiz questions outside an active attempt\n" +
			"(per D2L research §4.9), so the CLI does not expose question bodies.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	addQuizFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&quizCourse, "course", "", "Course OrgUnitId (required)")
		c.Flags().StringVarP(&quizEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
		c.Flags().BoolVar(&quizAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
		c.Flags().BoolVar(&quizJSON, "json", false, "Emit a JSON envelope instead of a report")
	}
	list := &cobra.Command{
		Use: "list", Short: "List quizzes in a course", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizList() },
	}
	get := &cobra.Command{
		Use: "get <quizId>", Short: "Show one quiz, with my attempts", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizGet(args[0]) },
	}
	atts := &cobra.Command{
		Use: "attempts <quizId>", Short: "List my attempts for a quiz", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizAttempts(args[0]) },
	}
	att := &cobra.Command{
		Use: "attempt <quizId> <attemptId>", Short: "Show one attempt's details", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizAttempt(args[0], args[1]) },
	}
	addQuizFlags(list)
	addQuizFlags(get)
	addQuizFlags(atts)
	addQuizFlags(att)
	get.Flags().BoolVar(&quizNoRelated, "no-related", false, "Fetch only the quiz; skip my attempts")
	list.Flags().BoolVar(&quizNoRelated, "no-related", false, "Skip the per-quiz attempts lookups")
	root.AddCommand(list, get, atts, att)
	rootCmd.AddCommand(root)
}

func requireQuizCourse() (string, error) {
	if quizCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return quizCourse, nil
}

func quizSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     quizEnvFile,
		AutoRefresh: quizAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func quizURL(courseID, path string) string {
	return fmt.Sprintf("%s/d2l/api/le/%s/%s/quizzes/%s", ua.D2LBase, dupdata.D2LVersion(), courseID, path)
}

func quizWindow(q Quiz) (start, end string) {
	if q.StartDate != nil {
		start = *q.StartDate
	}
	if q.EndDate != nil {
		end = *q.EndDate
	}
	return start, end
}

func quizDue(q Quiz) string {
	if q.DueDate != nil {
		return *q.DueDate
	}
	return ""
}

func quizStatus(q Quiz) string {
	if !q.IsActive {
		return "inactive"
	}
	start, end := quizWindow(q)
	if t, ok := render.Parse(start); ok && time.Now().Before(t) {
		return "upcoming"
	}
	if t, ok := render.Parse(end); ok && time.Now().After(t) {
		return "closed"
	}
	if t, ok := render.Parse(quizDue(q)); ok && time.Now().After(t) {
		return "overdue"
	}
	return "open"
}

func calcTypeLabel(n int) string {
	switch n {
	case 1:
		return "highest attempt"
	case 2:
		return "lowest attempt"
	case 3:
		return "average of all attempts"
	case 4:
		return "first attempt"
	case 5:
		return "last attempt"
	}
	return "—"
}

func timeLimitLabel(t QuizTimeLimit) string {
	if t.TimeLimitValue <= 0 {
		return "none"
	}
	label := fmt.Sprintf("%d min", t.TimeLimitValue)
	if t.IsEnforced {
		return label + " (enforced)"
	}
	return label + " (not enforced)"
}

func lateLabel(l QuizLateInfo) string {
	switch l.LateSubmissionOption {
	case 0:
		return "normal submission allowed"
	case 2:
		return "auto-submit when time expires"
	}
	return "—"
}

type quizRow struct {
	Quiz     Quiz          `json:"quiz"`
	Attempts []QuizAttempt `json:"attempts"`
}

func runQuizList() error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	items, err := dupdata.FetchJSONList[Quiz](quizURL(courseID, ""), ens.Jar)
	if err != nil {
		return err
	}

	rows := make([]quizRow, len(items))
	for i, q := range items {
		rows[i].Quiz = q
	}
	if !quizNoRelated {
		results := fetchAll(len(items), func(i int) ([]QuizAttempt, error) {
			return fetchQuizAttemptsFor(ens.Jar, courseID, items[i].QuizId.String())
		})
		for i, r := range results {
			if r.err == nil {
				rows[i].Attempts = r.value
			}
		}
	}

	if quizJSON {
		return render.PrintJSON(render.NewList(rows))
	}
	if len(rows) == 0 {
		fmt.Print(render.Empty("No quizzes in this course."))
		return nil
	}
	fmt.Print(render.Title("Quizzes · " + courseID))
	out := make([][]string, len(rows))
	for i, r := range rows {
		q := r.Quiz
		best := "—"
		if len(r.Attempts) > 0 {
			best = bestAttemptScore(r.Attempts)
		}
		out[i] = []string{
			q.QuizId.String(),
			render.StatusWord(quizStatus(q)),
			render.DateTime(quizDue(q)),
			q.AttemptsAllowed.Label(),
			timeLimitLabel(q.SubmissionTimeLimit),
			best,
			render.Truncate(q.Name, 44),
		}
	}
	headers := []string{"ID", "Status", "Due", "Attempts", "Time", "Best", "Name"}
	widths := []table.ColumnSpec{table.Fixed(8), table.Fixed(9), table.Fixed(22), table.Fixed(17), table.Fixed(19), table.Fixed(8), table.Flex(26)}
	if quizNoRelated {
		headers = []string{"ID", "Status", "Due", "Attempts", "Time", "Name"}
		widths = []table.ColumnSpec{table.Fixed(8), table.Fixed(9), table.Fixed(22), table.Fixed(17), table.Fixed(19), table.Flex(32)}
		for i := range out {
			out[i] = append(out[i][:5], out[i][6])
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: headers,
		Widths:  table.Widths(widths, table.TerminalWidth()),
		Rows:    out,
	}))
	fmt.Print(render.Hint("schooltools quiz get <id> --course %s   for instructions and your attempts", courseID))
	return nil
}

func bestAttemptScore(ats []QuizAttempt) string {
	best := ""
	for _, a := range ats {
		s := trimJSONNumber(a.Score)
		if s == "" {
			continue
		}
		if best == "" {
			best = s
			continue
		}
		if f, err := a.Score.Float64(); err == nil {
			if b, err2 := json.Number(best).Float64(); err2 == nil && f > b {
				best = s
			}
		}
	}
	return best
}

func fetchQuizAttemptsFor(jar *cookiejar.Jar, courseID, quizID string) ([]QuizAttempt, error) {
	raw := quizURL(courseID, quizID+"/attempts/") + "?userId=me"
	res, err := httpclient.FollowRedirects(raw, jar, httpclient.FetchOptions{
		Headers: map[string]string{"Accept": "application/json"},
	})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 403 {
		return nil, fmt.Errorf("D2L refused (403) — students cannot read this quiz's attempts")
	}
	if res.StatusCode != 200 {
		return nil, apiError(res, raw)
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return nil, err
	}
	var page struct {
		Items   []QuizAttempt `json:"Items"`
		Objects []QuizAttempt `json:"Objects"`
	}
	trimmed := strings.TrimLeft(string(body), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(body, &page.Items); err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, err
	}
	if page.Items != nil {
		return page.Items, nil
	}
	return page.Objects, nil
}

func runQuizGet(quizID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	var out Quiz
	if err := dupdata.FetchJSON(quizURL(courseID, quizID), ens.Jar, &out); err != nil {
		return err
	}
	rep := quizReport{Quiz: out, Attempts: []QuizAttempt{}}
	if !quizNoRelated {
		ats, aerr := fetchQuizAttemptsFor(ens.Jar, courseID, quizID)
		switch {
		case aerr != nil:
			rep.Notes = append(rep.Notes, "Could not read your attempts: "+aerr.Error())
		default:
			rep.Attempts = ats
		}
	}
	if quizJSON {
		return render.PrintJSON(rep)
	}
	fmt.Print(renderQuizReport(rep, courseID))
	return nil
}

func renderQuizReport(rep quizReport, courseID string) string {
	q := rep.Quiz
	var b strings.Builder
	b.WriteString(render.Title(q.Name))

	start, end := quizWindow(q)
	rows := []render.Row{
		{Key: "Quiz", Value: fmt.Sprintf("%s  ·  %s", q.QuizId.String(), render.StatusWord(quizStatus(q)))},
	}
	if due := quizDue(q); due != "" {
		rows = append(rows, render.Row{Key: "Due", Value: render.When(due)})
	}
	if start != "" || end != "" {
		rows = append(rows, render.Row{Key: "Open", Value: render.Window(start, end)})
	}
	rows = append(rows,
		render.Row{Key: "Attempts", Value: q.AttemptsAllowed.Label() + "  ·  " + calcTypeLabel(q.CalcTypeId)},
		render.Row{Key: "Time limit", Value: timeLimitLabel(q.SubmissionTimeLimit)},
		render.Row{Key: "Late work", Value: lateLabel(q.LateSubmissionInfo)},
	)
	if q.CategoryId != nil {
		rows = append(rows, render.Row{Key: "Category", Value: fmt.Sprintf("%d", *q.CategoryId)})
	}
	if q.GradeItemId != nil {
		rows = append(rows, render.Row{Key: "Grade item", Value: fmt.Sprintf("%d", *q.GradeItemId)})
	} else if q.AutoExportToGrades {
		rows = append(rows, render.Row{Key: "Grade item", Value: "exports to grades"})
	} else {
		rows = append(rows, render.Row{Key: "Grade item", Value: render.Muted("not in the gradebook")})
	}
	b.WriteString(render.KV(rows...))

	if d := q.Description.Plain(); d != "" {
		b.WriteString(render.Section("About this quiz"))
		b.WriteString(render.Wrap(d, render.Width()) + "\n")
	}
	if i := q.Instructions.Plain(); i != "" {
		b.WriteString(render.Section("Instructions"))
		b.WriteString(render.Wrap(i, render.Width()) + "\n")
	}

	if !quizNoRelated {
		b.WriteString(render.Section("Your attempts"))
		b.WriteString(attemptsSection(rep.Attempts))
	}

	if len(rep.Notes) > 0 {
		b.WriteString(render.Section("Notes"))
		for _, n := range rep.Notes {
			for _, line := range strings.Split(render.Wrap(n, render.Width()-2), "\n") {
				b.WriteString(render.Note("%s", line))
			}
		}
	}

	attemptsBlocked := quizNoRelated || (len(rep.Attempts) == 0 && len(rep.Notes) > 0)
	b.WriteString(render.Section("Next"))
	if attemptsBlocked {
		b.WriteString(render.Hint("schooltools grade --course %s", courseID))
	} else {
		b.WriteString(render.Hint("schooltools quiz attempts %s --course %s", q.QuizId.String(), courseID))
	}
	return b.String()
}

func attemptsSection(ats []QuizAttempt) string {
	if len(ats) == 0 {
		return render.Empty("No attempts yet.")
	}
	var b strings.Builder
	b.WriteString("  " + render.Muted(fmt.Sprintf("%-6s %-22s %-22s %s", "Attempt", "Started", "Completed", "Score")) + "\n")
	for _, a := range ats {
		score := trimJSONNumber(a.Score)
		if score == "" {
			score = render.Muted("—")
		} else {
			score = render.Accent(score)
		}
		done := render.When(a.StartTime())
		if !a.Done() {
			done = render.When(a.StartTime()) + "  " + render.Warn("in progress")
		}
		b.WriteString(fmt.Sprintf("  %-6s %-44s %s\n",
			fmt.Sprintf("#%d", a.AttemptNumber),
			done,
			score))
	}
	if best := bestAttemptScore(ats); best != "" {
		b.WriteString(render.KV(render.Row{Key: "Best", Value: render.Accent(best)}))
	}
	return b.String()
}

func runQuizAttempts(quizID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	items, err := fetchQuizAttemptsFor(ens.Jar, courseID, quizID)
	if err != nil {
		return err
	}
	if quizJSON {
		return render.PrintJSON(render.NewList(items))
	}
	fmt.Print(render.Title(fmt.Sprintf("My attempts · quiz %s", quizID)))
	fmt.Print(attemptsSection(items))
	if len(items) > 0 {
		fmt.Print(render.Hint("schooltools quiz attempt %s <attemptId> --course %s", quizID, courseID))
	}
	return nil
}

func runQuizAttempt(quizID, attemptID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	var out QuizAttempt
	if err := dupdata.FetchJSON(quizURL(courseID, quizID+"/attempts/"+attemptID), ens.Jar, &out); err != nil {
		return err
	}
	if quizJSON {
		return render.PrintJSON(out)
	}
	fmt.Print(render.Title(fmt.Sprintf("Attempt %s · quiz %s", attemptID, quizID)))
	rows := []render.Row{
		{Key: "Attempt", Value: fmt.Sprintf("#%d", out.AttemptNumber)},
		{Key: "Started", Value: render.When(out.StartTime())},
	}
	if out.Done() {
		rows = append(rows, render.Row{Key: "Completed", Value: render.When(out.EndTime())})
	} else {
		rows = append(rows, render.Row{Key: "Completed", Value: render.Warn("in progress")})
	}
	if s := trimJSONNumber(out.Score); s != "" {
		rows = append(rows, render.Row{Key: "Score", Value: render.Accent(s)})
	} else {
		rows = append(rows, render.Row{Key: "Score", Value: render.Muted("not released")})
	}
	if out.AttemptSubmissionTimeLimit > 0 {
		rows = append(rows, render.Row{Key: "Time limit", Value: fmt.Sprintf("%d min", out.AttemptSubmissionTimeLimit)})
	}
	if out.IsPublished {
		rows = append(rows, render.Row{Key: "Feedback", Value: render.OK("published")})
	} else {
		rows = append(rows, render.Row{Key: "Feedback", Value: render.Warn("not published yet")})
	}
	fmt.Print(render.KV(rows...))
	if note := out.AttemptFeedback.Plain(); note != "" {
		fmt.Print(render.Section("Feedback on this attempt"))
		fmt.Println(render.Wrap(note, render.Width()))
	}
	return nil
}
