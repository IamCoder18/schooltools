// Rare per-course surfaces from plan §5.8 / §5.9 / §5.10. Each command
// takes `--course <orgUnitId>` to scope the request to one course.
package cmd

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http/cookiejar"
	"os"
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

// ───────────────────────────── news ─────────────────────────────

var (
	newsCourse   string
	newsEnvFile  string
	newsAutoRef  bool
	newsJSON     bool
	newsSince    string
	newsUntil    string
	newsBodyFmt  string
	newsCrossTry bool
)

type NewsItem struct {
	Id               json.Number `json:"Id"`
	OrgUnitId        int         `json:"OrgUnitId,omitempty"`
	Title            string      `json:"Title"`
	Body             any         `json:"Body"`
	StartDate        string      `json:"StartDate"`
	EndDate          string      `json:"EndDate,omitempty"`
	IsPinned         bool        `json:"IsPinned"`
	IsGlobal         bool        `json:"IsGlobal,omitempty"`
	HasAttachment    bool        `json:"HasAttachment"`
	CreatedDate      string      `json:"CreatedDate"`
	LastModifiedDate string      `json:"LastModifiedDate"`
	CreatedBy        *int        `json:"CreatedBy"`
	IsHidden         bool        `json:"IsHidden"`

	// Fields added at LE 1.97+ — surfaced when the tenant is on a new
	// enough version. Older versions just leave them at the zero value.
	PinnedDate                *string          `json:"PinnedDate"`
	IsPublished               bool             `json:"IsPublished"`
	IsStartDateShown          bool             `json:"IsStartDateShown"`
	IsAuthorInfoShown         bool             `json:"IsAuthorInfoShown"`
	ShowOnlyInCourseOfferings bool             `json:"ShowOnlyInCourseOfferings"`
	SortOrder                 int              `json:"SortOrder"`
	Attachments               []NewsAttachment `json:"Attachments,omitempty"`
}

type NewsAttachment struct {
	FileId   int    `json:"FileId"`
	FileName string `json:"FileName"`
	Size     int64  `json:"Size"`
}

func (n NewsItem) Date() string {
	if n.StartDate != "" {
		return n.StartDate
	}
	if n.CreatedDate != "" {
		return n.CreatedDate
	}
	return n.LastModifiedDate
}

// NewsBody holds the {Html,Text} shape we get from D2L. Used by --json to
// flatten to a string via --body-format text|html|both.
type NewsBody struct {
	Html string `json:"Html"`
	Text string `json:"Text"`
}

func init() {
	root := &cobra.Command{
		Use:   "news",
		Short: "Course announcements and cross-course news (rare)",
		Long: "Course announcements. Usually read in the D2L web UI; this\n" +
			"command exists for completeness and for archival workflows.\n\n" +
			"`news --course <id>` lists per-course news. `news get <newsId>`\n" +
			"shows one item. `news attachment <newsId> <fileId>` downloads an\n" +
			"attachment. `news list [--course ID] [--since …] [--until …]` is\n" +
			"the alias for the root command with discoverable subcommands. `news`\n" +
			"with no `--course` returns cross-course news (aggregated per-course).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsList() },
	}
	registerNewsFlags(root)

	list := &cobra.Command{
		Use:   "list",
		Short: "List announcements (alias for the root command, keeps the discoverability story uniform)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return runNewsList() },
	}
	// Re-register every shared flag on each subcommand. AddFlagSet only
	// wires flags the cobra command tree understands, but `--course` must
	// bind to the same package-level variables; reusing `registerNewsFlags`
	// does that. Cobra's `AddFlagSet` on a different command does not
	// re-wire those bindings, which is why the prior form silently dropped
	// `--course` on `news list`.
	registerNewsFlags(list)

	get := &cobra.Command{
		Use: "get <newsId>", Short: "Show a single news item", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsGet(args[0]) },
	}
	registerNewsFlags(get)
	get.Flags().BoolVar(&newsCrossTry, "cross-try", true, "When --course is omitted, try every enrolled course and return the first hit")

	att := &cobra.Command{
		Use: "attachment <newsId> <fileId>", Short: "Download a news attachment", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsAttachment(args[0], args[1]) },
	}
	registerNewsFlags(att)
	att.Flags().BoolVar(&newsCrossTry, "cross-try", true, "When --course is omitted, try every enrolled course")
	att.Flags().StringVar(&asgOut, "out", defaultHome(), "Output directory")
	att.Flags().StringVar(&asgName, "name", "", "Override output filename")

	root.AddCommand(list, get, att)
	rootCmd.AddCommand(root)
}

// registerNewsFlags is the common flag set for both `news` and `news list`.
// Registered as regular flags (not PersistentFlags) so cobra exposes them on
// every subcommand without us having to wire AddFlagSet, which doesn't
// rebind the shared package variables.
func registerNewsFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&newsCourse, "course", "", "Course OrgUnitId (omit for cross-course)")
	cmd.Flags().StringVarP(&newsEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	cmd.Flags().BoolVar(&newsAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	cmd.Flags().BoolVar(&newsJSON, "json", false, "Emit a JSON envelope instead of a table")
	cmd.Flags().StringVar(&newsSince, "since", "", "ISO 8601 lower bound for cross-course news")
	cmd.Flags().StringVar(&newsUntil, "until", "", "ISO 8601 upper bound for cross-course news")
	cmd.Flags().StringVar(&newsBodyFmt, "body-format", "text", "When --json: text|html|both (flatten Body accordingly)")
}

func newsSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     newsEnvFile,
		AutoRefresh: newsAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func newsCourseIDs(jar *cookiejar.Jar) ([]string, error) {
	csv, err := dupdata.CourseOrgIDsCSV(jar)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(csv, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// newsListJSONItem is the --json shape: an envelope-friendly row with Body
// flattened to a single string. source is the OrgUnitId the item came from.
type newsListJSONItem struct {
	Id            string `json:"id"`
	OrgUnitId     int    `json:"orgUnitId"`
	Title         string `json:"title"`
	Body          string `json:"body,omitempty"`
	BodyHtml      string `json:"bodyHtml,omitempty"`
	StartDate     string `json:"startDate"`
	EndDate       string `json:"endDate,omitempty"`
	IsPinned      bool   `json:"isPinned"`
	IsPublished   bool   `json:"isPublished"`
	HasAttachment bool   `json:"hasAttachment"`
	Source        string `json:"sourceCourseId,omitempty"`
}

func flattenBody(n NewsItem, format string) (string, string) {
	if n.Body == nil {
		return "", ""
	}
	var b NewsBody
	switch v := n.Body.(type) {
	case NewsBody:
		b = v
	case map[string]any:
		if h, ok := v["Html"].(string); ok {
			b.Html = h
		}
		if t, ok := v["Text"].(string); ok {
			b.Text = t
		}
	default:
		// Fallback: serialise the raw shape.
		raw, _ := json.Marshal(n.Body)
		return "", string(raw)
	}
	switch strings.ToLower(format) {
	case "html":
		return "", b.Html
	case "both":
		return b.Text, b.Html
	default:
		if b.Text != "" {
			return b.Text, ""
		}
		return htmlToText(b.Html), b.Html
	}
}

// htmlToText is a deliberately conservative HTML→text conversion. We
// only drop tags, collapse runs of whitespace, and decode the handful
// of entities that commonly appear in D2L news bodies (& < >
// " ' &nbsp;). Anything richer (tables, images, embedded
// styles) is left as-is in the HTML, which the caller can request
// explicitly with --body-format html.
func htmlToText(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteRune(' ')
		case inTag:
			// skip tag contents
		default:
			b.WriteRune(r)
		}
	}
	r := strings.NewReplacer(
		`&`, "&",
		`<`, "<",
		`>`, ">",
		"\"", `"`,
		`&apos;`, "'",
		`&nbsp;`, " ",
	)
	out := r.Replace(b.String())
	out = html.UnescapeString(out)
	out = strings.ReplaceAll(out, "\u00a0", " ")
	out = collapseSpaces(out)
	return strings.TrimSpace(out)
}

func collapseSpaces(s string) string {
	var b strings.Builder
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\n' || r == '\t' || r == '\r' {
			if !prevSpace {
				b.WriteRune(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

func runNewsList() error {
	if err := validateNewsBounds(); err != nil {
		return err
	}
	ens, err := newsSession()
	if err != nil {
		return err
	}
	if newsCourse == "" {
		return runNewsListCrossCourse(ens.Jar)
	}
	return runNewsListOneCourse(ens.Jar, newsCourse)
}

func runNewsListOneCourse(jar *cookiejar.Jar, courseID string) error {
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/", ua.D2LBase, dupdata.D2LVersion(), courseID)
	raw, _ = dupdata.AppendQuery(raw, "since", newsSince)
	raw, _ = dupdata.AppendQuery(raw, "until", newsUntil)
	items, err := dupdata.FetchJSONList[NewsItem](raw, jar)
	if err != nil {
		return err
	}
	if newsJSON {
		out := map[string]any{
			"courseId": courseID,
			"since":    newsSince,
			"until":    newsUntil,
			"count":    len(items),
			"news":     newsListJSON(items, courseID),
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No announcements in this course."))
		return nil
	}
	fmt.Print(render.Title("Announcements · " + courseID))
	fmt.Print(newsTable(items))
	fmt.Print(render.Hint("schooltools news get <id> --course %s", courseID))
	return nil
}

func newsFlags(n NewsItem) string {
	if !n.IsPublished {
		return render.Warn("draft")
	}
	if n.IsPinned {
		return render.Accent("pinned")
	}
	return render.Muted("published")
}

func newsTable(items []NewsItem) string {
	rows := make([][]string, len(items))
	for i, n := range items {
		rows[i] = []string{
			n.Id.String(),
			render.Date(n.Date()),
			render.Truncate(render.Rel(n.Date()), 12),
			newsFlags(n),
			n.Title,
		}
	}
	return table.Render(table.Options{
		Headers: []string{"ID", "Posted", "Ago", "Status", "Title"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Fixed(16), table.Fixed(12), table.Fixed(11), table.Flex(34)}, table.TerminalWidth()),
		Rows:    rows,
	})
}

func runNewsListCrossCourse(jar *cookiejar.Jar) error {
	courseIDs, err := newsCourseIDs(jar)
	if err != nil {
		return err
	}
	type flat struct {
		news NewsItem
		org  int
	}
	var all []flat
	successes := 0
	for _, idStr := range courseIDs {
		orgID, _ := strconv.Atoi(idStr)
		raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/", ua.D2LBase, dupdata.D2LVersion(), idStr)
		raw, _ = dupdata.AppendQuery(raw, "since", newsSince)
		raw, _ = dupdata.AppendQuery(raw, "until", newsUntil)
		items, err := dupdata.FetchJSONList[NewsItem](raw, jar)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[news] skipping course %s: %v\n", idStr, err)
			continue
		}
		successes++
		for _, n := range items {
			if !withinRange(n, newsSince, newsUntil) {
				continue
			}
			all = append(all, flat{news: n, org: orgID})
		}
	}
	if successes == 0 {
		return fmt.Errorf("news list: every per-course fetch failed (see stderr)")
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].news.StartDate > all[j].news.StartDate
	})
	if newsJSON {
		grouped := make(map[string][]newsListJSONItem, len(courseIDs))
		for _, cid := range courseIDs {
			grouped[cid] = nil
		}
		for _, f := range all {
			cid := fmt.Sprintf("%d", f.org)
			grouped[cid] = append(grouped[cid], newsListJSONOne(f.news, cid)...)
		}
		flattened := make([]newsListJSONItem, 0, len(all))
		for _, f := range all {
			flattened = append(flattened, newsListJSONOne(f.news, fmt.Sprintf("%d", f.org))...)
		}
		out := map[string]any{
			"courseCount": len(courseIDs),
			"courses":     courseIDs,
			"since":       newsSince,
			"until":       newsUntil,
			"count":       len(all),
			"news":        flattened,
			"perCourse":   grouped,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(all) == 0 {
		fmt.Print(render.Empty("No announcements across any of your courses."))
		return nil
	}
	fmt.Print(render.Title("Announcements"))
	rows := make([][]string, len(all))
	for i, f := range all {
		rows[i] = []string{
			f.news.Id.String(),
			render.Truncate(render.Rel(f.news.Date()), 12),
			render.Date(f.news.Date()),
			newsFlags(f.news),
			render.Truncate(fmt.Sprintf("%d", f.org), 10),
			f.news.Title,
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"ID", "Ago", "Posted", "Status", "Course", "Title"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Fixed(12), table.Fixed(16), table.Fixed(11), table.Fixed(10), table.Flex(32)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	fmt.Print(render.Hint("schooltools news get <id> --course <orgUnitId>"))
	return nil
}

func newsListJSON(items []NewsItem, courseID string) []newsListJSONItem {
	out := make([]newsListJSONItem, 0, len(items))
	for _, n := range items {
		out = append(out, newsListJSONOne(n, courseID)...)
	}
	return out
}

func newsListJSONOne(n NewsItem, sourceCourse string) []newsListJSONItem {
	text, html := flattenBody(n, newsBodyFmt)
	sourceInt := n.OrgUnitId
	if sourceCourse != "" {
		if v, err := strconv.Atoi(sourceCourse); err == nil {
			sourceInt = v
		}
	}
	return []newsListJSONItem{{
		Id:            n.Id.String(),
		OrgUnitId:     sourceInt,
		Title:         n.Title,
		Body:          text,
		BodyHtml:      html,
		StartDate:     n.StartDate,
		EndDate:       n.EndDate,
		IsPinned:      n.IsPinned,
		IsPublished:   n.IsPublished,
		HasAttachment: len(n.Attachments) > 0,
		Source:        sourceCourse,
	}}
}

// withinRange filters by the newsUntil bound locally, since D2L's `until`
// parameter is not always honoured by every endpoint. We compare parsed
// timestamps so equivalent instants with different serialisations
// (fractional seconds, varying timezone offsets) compare correctly; a
// fallback to ISO-8601 string comparison handles the rare case of an
// unparseable item StartDate.
func withinRange(n NewsItem, since, until string) bool {
	if since != "" {
		if ts, err := parseNewsDate(n.StartDate); err == nil {
			if sinceTS, err := parseNewsDate(since); err == nil && ts.Before(sinceTS) {
				return false
			}
		} else if n.StartDate < since {
			return false
		}
	}
	if until != "" {
		if ts, err := parseNewsDate(n.StartDate); err == nil {
			if untilTS, err := parseNewsDate(until); err == nil && ts.After(untilTS) {
				return false
			}
		} else if n.StartDate > until {
			return false
		}
	}
	return true
}

// validateNewsBounds rejects malformed --since / --until values up front
// so withinRange never silently includes out-of-range items behind a
// successful result.
func validateNewsBounds() error {
	if newsSince != "" {
		if _, err := parseNewsDate(newsSince); err != nil {
			return fmt.Errorf("invalid --since %q: %w", newsSince, err)
		}
	}
	if newsUntil != "" {
		if _, err := parseNewsDate(newsUntil); err != nil {
			return fmt.Errorf("invalid --until %q: %w", newsUntil, err)
		}
	}
	return nil
}

// parseNewsDate accepts the ISO-8601 forms D2L emits (with or without
// fractional seconds, with a Z or a numeric offset). Returns the
// zero time on failure so the caller can fall back to string compare.
func parseNewsDate(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty date")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("unrecognised date %q", s)
}

func runNewsGet(newsID string) error {
	ens, err := newsSession()
	if err != nil {
		return err
	}
	if newsCourse == "" && newsCrossTry {
		courseIDs, err := newsCourseIDs(ens.Jar)
		if err != nil {
			return err
		}
		var lastErr error
		for _, cid := range courseIDs {
			item, err := fetchNewsItem(ens.Jar, cid, newsID)
			if err == nil {
				return printNewsItem(item, cid)
			}
			lastErr = err
		}
		if lastErr != nil {
			return fmt.Errorf("news get %s not found across any enrolled course (last error: %w)", newsID, lastErr)
		}
		return fmt.Errorf("news get %s not found", newsID)
	}
	if newsCourse == "" {
		return fmt.Errorf("--course <orgUnitId> is required for `news get` (or omit it and --cross-try will search)")
	}
	item, err := fetchNewsItem(ens.Jar, newsCourse, newsID)
	if err != nil {
		return err
	}
	return printNewsItem(item, newsCourse)
}

func fetchNewsItem(jar *cookiejar.Jar, courseID, newsID string) (NewsItem, error) {
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, newsID)
	var out NewsItem
	err := dupdata.FetchJSON(raw, jar, &out)
	return out, err
}

func printNewsItem(n NewsItem, sourceCourse string) error {
	if newsJSON {
		text, html := flattenBody(n, newsBodyFmt)
		sourceInt := n.OrgUnitId
		if v, err := strconv.Atoi(sourceCourse); err == nil {
			sourceInt = v
		}
		out := map[string]any{
			"id":            n.Id.String(),
			"orgUnitId":     sourceInt,
			"sourceCourse":  sourceCourse,
			"title":         n.Title,
			"body":          text,
			"bodyHtml":      html,
			"startDate":     n.StartDate,
			"endDate":       n.EndDate,
			"isPinned":      n.IsPinned,
			"isPublished":   n.IsPublished,
			"hasAttachment": len(n.Attachments) > 0,
			"attachments":   n.Attachments,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	var b strings.Builder
	b.WriteString(render.Title(n.Title))
	b.WriteString(render.KV(
		render.Row{Key: "Post", Value: n.Id.String()},
		render.Row{Key: "Posted", Value: render.When(n.Date())},
		render.Row{Key: "Status", Value: newsFlags(n)},
		render.Row{Key: "Course", Value: sourceCourse},
	))
	if body := newsBodyText(n); body != "" {
		b.WriteString(render.Section("Body"))
		b.WriteString(render.Wrap(body, render.Width()) + "\n")
	}
	if len(n.Attachments) > 0 {
		b.WriteString(render.Section("Attachments"))
		for _, a := range n.Attachments {
			b.WriteString(render.Bullets(fmt.Sprintf("%s  (%s)   → schooltools news attachment %s %d",
				a.FileName, render.HumanBytes(a.Size), n.Id.String(), a.FileId)))
		}
	}
	b.WriteString(render.Section("Next"))
	b.WriteString(render.Hint("schooltools news --course %s", sourceCourse))
	fmt.Print(b.String())
	return nil
}

func newsBodyText(n NewsItem) string {
	text, _ := flattenBody(n, "text")
	return text
}

func runNewsAttachment(newsID, fileID string) error {
	ens, err := newsSession()
	if err != nil {
		return err
	}
	if newsCourse == "" && newsCrossTry {
		courseIDs, err := newsCourseIDs(ens.Jar)
		if err != nil {
			return err
		}
		for _, cid := range courseIDs {
			raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/%s/attachments/%s",
				ua.D2LBase, dupdata.D2LVersion(), cid, newsID, fileID)
			if err := asgDownloadFile(ens.Jar, raw, fmt.Sprintf("news-%s-%s", newsID, fileID)); err == nil {
				return nil
			}
		}
		return fmt.Errorf("news attachment %s/%s not found in any enrolled course", newsID, fileID)
	}
	if newsCourse == "" {
		return fmt.Errorf("--course <orgUnitId> is required for `news attachment` (or omit and --cross-try)")
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/%s/attachments/%s",
		ua.D2LBase, dupdata.D2LVersion(), newsCourse, newsID, fileID)
	return asgDownloadFile(ens.Jar, raw, fmt.Sprintf("news-%s-%s", newsID, fileID))
}

// ───────────────────────────── grade ─────────────────────────────

var (
	gradeCourse  string
	gradeEnvFile string
	gradeAutoRef bool
	gradeJSON    bool
	gradeFinal   bool
)

type GradeValue struct {
	GradeObjectId     int     `json:"GradeObjectId"`
	GradeObjectName   string  `json:"GradeObjectName"`
	GradeObjectType   int     `json:"GradeObjectType"`
	PointsNumerator   float64 `json:"PointsNumerator"`
	PointsDenominator float64 `json:"PointsDenominator"`
	DisplayedGrade    string  `json:"DisplayedGrade"`
	GradeSchemeSymbol string  `json:"GradeSchemeSymbol"`
	IsReleased        bool    `json:"IsReleased"`
}

func init() {
	root := &cobra.Command{
		Use:   "grade",
		Short: "Numeric grades for a course or cross-course finals (rare)",
		Long: "Numeric grades. Per-course (`grade --course <id>`) shows every\n" +
			"grade item for that course; `--final` shows the course final;\n" +
			"`grade get <gradeObjectId>` shows one. `grade` with no `--course`\n" +
			"returns cross-course finals (capped at 100 per D2L research §3.4).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runGradeRoot() },
	}
	root.Flags().StringVar(&gradeCourse, "course", "", "Course OrgUnitId (omit for cross-course finals)")
	root.Flags().BoolVar(&gradeFinal, "final", false, "Show only the course final grade")
	root.Flags().StringVarP(&gradeEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&gradeAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&gradeJSON, "json", false, "Emit raw JSON")

	get := &cobra.Command{
		Use: "get <gradeObjectId>", Short: "Show one grade item", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runGradeGet(args[0]) },
	}
	get.Flags().StringVar(&gradeCourse, "course", "", "Course OrgUnitId (required)")
	get.Flags().StringVarP(&gradeEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	get.Flags().BoolVar(&gradeAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	get.Flags().BoolVar(&gradeJSON, "json", false, "Emit raw JSON")

	root.AddCommand(get)
	rootCmd.AddCommand(root)
}

func gradeSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     gradeEnvFile,
		AutoRefresh: gradeAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func runGradeRoot() error {
	ens, err := gradeSession()
	if err != nil {
		return err
	}
	if gradeCourse == "" {
		// Cross-course finals (capped at 100 per D2L research §3.4). The
		// endpoint requires a non-empty `orgUnitIdsCSV=` parameter.
		orgs, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		raw := fmt.Sprintf("%s/d2l/api/le/%s/grades/final/values/myGradeValues/?orgUnitIdsCSV=%s",
			ua.D2LBase, dupdata.D2LVersion(), orgs)
		items, err := dupdata.FetchJSONList[GradeValue](raw, ens.Jar)
		if err != nil {
			return err
		}
		if gradeJSON {
			return render.PrintJSON(render.NewList(items))
		}
		if len(items) == 0 {
			fmt.Print(render.Empty("No course finals released yet."))
			return nil
		}
		fmt.Print(render.Title("Course finals"))
		fmt.Print(gradeTable(items))
		return nil
	}
	if gradeFinal {
		raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/grades/final/values/myGradeValue",
			ua.D2LBase, dupdata.D2LVersion(), gradeCourse)
		var out GradeValue
		if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
			return err
		}
		if gradeJSON {
			return render.PrintJSON(out)
		}
		fmt.Print(render.Title("Course final · " + gradeCourse))
		fmt.Print(gradeKV(out))
		return nil
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/grades/values/myGradeValues/",
		ua.D2LBase, dupdata.D2LVersion(), gradeCourse)
	items, err := dupdata.FetchJSONList[GradeValue](raw, ens.Jar)
	if err != nil {
		return err
	}
	if gradeJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No grades found for this course."))
		return nil
	}
	fmt.Print(render.Title("Grades · " + gradeCourse))
	fmt.Print(gradeTable(items))
	return nil
}

func gradeScore(g GradeValue) string {
	if g.DisplayedGrade != "" {
		return g.DisplayedGrade
	}
	return fmt.Sprintf("%g / %g", g.PointsNumerator, g.PointsDenominator)
}

func gradeTable(items []GradeValue) string {
	rows := make([][]string, len(items))
	for i, g := range items {
		status := render.Muted("not released")
		if g.IsReleased {
			status = render.OK("released")
		}
		rows[i] = []string{
			strconv.Itoa(g.GradeObjectId),
			g.GradeObjectName,
			render.Accent(gradeScore(g)),
			fmt.Sprintf("%g / %g", g.PointsNumerator, g.PointsDenominator),
			g.GradeSchemeSymbol,
			status,
		}
	}
	return table.Render(table.Options{
		Headers: []string{"ID", "Item", "Score", "Out of", "Symbol", "Status"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Flex(28), table.Fixed(12), table.Fixed(14), table.Fixed(9), table.Fixed(14)}, table.TerminalWidth()),
		Rows:    rows,
	})
}

func gradeKV(g GradeValue) string {
	rows := []render.Row{
		{Key: "Score", Value: render.Accent(gradeScore(g))},
		{Key: "Out of", Value: fmt.Sprintf("%g points", g.PointsDenominator)},
	}
	if g.GradeSchemeSymbol != "" {
		rows = append(rows, render.Row{Key: "Symbol", Value: g.GradeSchemeSymbol})
	}
	if g.IsReleased {
		rows = append(rows, render.Row{Key: "Status", Value: render.OK("released")})
	} else {
		rows = append(rows, render.Row{Key: "Status", Value: render.Warn("not released yet")})
	}
	return render.KV(rows...)
}

func runGradeGet(objectID string) error {
	if gradeCourse == "" {
		return fmt.Errorf("--course <orgUnitId> is required for `grade get`")
	}
	ens, err := gradeSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/grades/%s/values/myGradeValue",
		ua.D2LBase, dupdata.D2LVersion(), gradeCourse, objectID)
	var out GradeValue
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	if gradeJSON {
		return render.PrintJSON(out)
	}
	fmt.Print(render.Title(out.GradeObjectName))
	fmt.Print(gradeKV(out))
	return nil
}

// ─────────────────────────── discussion ───────────────────────────

var (
	disCourse  string
	disEnvFile string
	disAutoRef bool
	disJSON    bool
)

type Forum struct {
	Id           json.Number `json:"Id"`
	Name         string      `json:"Name"`
	Description  any         `json:"Description"`
	AllowAnon    bool        `json:"AllowAnonymous"`
	IsHidden     bool        `json:"IsHidden"`
	NumTopics    int         `json:"NumTopics"`
	NumPosts     int         `json:"NumPosts"`
	LastPostDate string      `json:"LastPostDate"`
}

type ForumTopic struct {
	Id           int    `json:"Id"`
	Name         string `json:"Name"`
	NumPosts     int    `json:"NumPosts"`
	LastPostDate string `json:"LastPostDate"`
	IsPinned     bool   `json:"IsPinned"`
	IsLocked     bool   `json:"IsLocked"`
}

func init() {
	root := &cobra.Command{
		Use:   "discussion",
		Short: "Forums → topics → posts in a course (rare; archival only)",
		Long: "Discussion forums. Useful for archival, not day-to-day.\n\n" +
			"`discussion --course <id>` lists forums; `discussion forum <id>`\n" +
			"lists topics; `discussion post <topicId>` lists posts (uses\n" +
			"page-number pagination, per D2L research §2.2).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runDisList() },
	}
	root.Flags().StringVar(&disCourse, "course", "", "Course OrgUnitId (required)")
	root.Flags().StringVarP(&disEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&disAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&disJSON, "json", false, "Emit raw JSON instead of a table")

	fr := &cobra.Command{
		Use: "forum <forumId>", Short: "List topics in a forum", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runDisForum(args[0]) },
	}
	fr.Flags().StringVar(&disCourse, "course", "", "Course OrgUnitId (required)")
	fr.Flags().BoolVar(&disJSON, "json", false, "Emit raw JSON")
	fr.Flags().StringVarP(&disEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	fr.Flags().BoolVar(&disAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")

	pst := &cobra.Command{
		Use: "post <topicId>", Short: "List posts in a topic (page-number paginated)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runDisPost(args[0]) },
	}
	pst.Flags().StringVar(&disCourse, "course", "", "Course OrgUnitId (required)")
	pst.Flags().BoolVar(&disJSON, "json", false, "Emit raw JSON")
	pst.Flags().StringVarP(&disEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	pst.Flags().BoolVar(&disAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")

	root.AddCommand(fr, pst)
	rootCmd.AddCommand(root)
}

func disSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     disEnvFile,
		AutoRefresh: disAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func requireDisCourse() (string, error) {
	if disCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return disCourse, nil
}

func runDisList() error {
	courseID, err := requireDisCourse()
	if err != nil {
		return err
	}
	ens, err := disSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/discussions/forums/",
		ua.D2LBase, dupdata.D2LVersion(), courseID)
	items, err := dupdata.FetchJSONList[Forum](raw, ens.Jar)
	if err != nil {
		return err
	}
	if disJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No forums in this course."))
		return nil
	}
	fmt.Print(render.Title("Discussion forums · " + courseID))
	rows := make([][]string, len(items))
	for i, f := range items {
		status := render.Muted("open")
		if f.IsHidden {
			status = render.Bad("hidden")
		}
		rows[i] = []string{
			f.Id.String(),
			status,
			render.Count(f.NumTopics, "topic"),
			render.Count(f.NumPosts, "post"),
			render.Truncate(render.Rel(f.LastPostDate), 12),
			f.Name,
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"ID", "Status", "Topics", "Posts", "Last post", "Forum"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Fixed(9), table.Fixed(11), table.Fixed(11), table.Fixed(12), table.Flex(34)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	fmt.Print(render.Hint("schooltools discussion forum <forumId> --course %s", courseID))
	return nil
}

func runDisForum(forumID string) error {
	courseID, err := requireDisCourse()
	if err != nil {
		return err
	}
	ens, err := disSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/discussions/forums/%s/topics/?pageSize=50&pageNumber=1",
		ua.D2LBase, dupdata.D2LVersion(), courseID, forumID)
	items, err := dupdata.FetchJSONList[ForumTopic](raw, ens.Jar)
	if err != nil {
		return err
	}
	if disJSON {
		return render.PrintJSON(render.NewList(items))
	}
	if len(items) == 0 {
		fmt.Print(render.Empty("No topics in this forum."))
		return nil
	}
	fmt.Print(render.Title("Topics · forum " + forumID))
	rows := make([][]string, len(items))
	for i, t := range items {
		status := render.Muted("open")
		switch {
		case t.IsLocked:
			status = render.Bad("locked")
		case t.IsPinned:
			status = render.Accent("pinned")
		}
		rows[i] = []string{
			strconv.Itoa(t.Id),
			status,
			render.Count(t.NumPosts, "post"),
			render.Truncate(render.Rel(t.LastPostDate), 12),
			t.Name,
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"ID", "Status", "Posts", "Last post", "Topic"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(9), table.Fixed(10), table.Fixed(11), table.Fixed(12), table.Flex(38)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	fmt.Print(render.Hint("schooltools discussion post <topicId> --course %s", courseID))
	return nil
}

func runDisPost(topicID string) error {
	courseID, err := requireDisCourse()
	if err != nil {
		return err
	}
	ens, err := disSession()
	if err != nil {
		return err
	}
	// Topic ID alone is not enough — posts live under a forum. The plan
	// is ambiguous on this; use the per-topic route which the API exposes.
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/discussions/forums/0/topics/%s/posts/?pageSize=20&pageNumber=1",
		ua.D2LBase, dupdata.D2LVersion(), courseID, topicID)
	var out json.RawMessage
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		raw2 := fmt.Sprintf("%s/d2l/api/le/%s/%s/discussions/topics/%s/posts/?pageSize=20&pageNumber=1",
			ua.D2LBase, dupdata.D2LVersion(), courseID, topicID)
		if err2 := dupdata.FetchJSON(raw2, ens.Jar, &out); err2 != nil {
			return err
		}
	}
	if disJSON {
		fmt.Println(string(out))
		return nil
	}
	posts, ok := decodeDiscussionPosts(out)
	if !ok {
		fmt.Println(string(out))
		return nil
	}
	if len(posts) == 0 {
		fmt.Print(render.Empty("No posts in this topic."))
		return nil
	}
	fmt.Print(render.Title("Posts · topic " + topicID))
	for _, p := range posts {
		fmt.Print(renderPost(p))
	}
	return nil
}

type DiscussionAuthor struct {
	UserId      json.Number `json:"UserId"`
	DisplayName string      `json:"DisplayName"`
	Name        string      `json:"Name"`
}

func (a *DiscussionAuthor) Label() string {
	if a == nil {
		return ""
	}
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Name
}

type DiscussionPost struct {
	Id             json.Number       `json:"Id"`
	ParentId       json.Number       `json:"ParentId"`
	ThreadId       json.Number       `json:"ThreadId"`
	Subject        string            `json:"Subject"`
	Message        RichText          `json:"Message"`
	PostingDate    string            `json:"PostingDate"`
	LastEditedDate string            `json:"LastEditedDate"`
	IsRead         bool              `json:"IsRead"`
	NumReplies     int               `json:"NumReplies"`
	Depth          int               `json:"Depth"`
	Author         *DiscussionAuthor `json:"Author"`
}

func decodeDiscussionPosts(raw json.RawMessage) ([]DiscussionPost, bool) {
	trimmed := strings.TrimLeft(string(raw), " \t\r\n")
	if strings.HasPrefix(trimmed, "[") {
		var items []DiscussionPost
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, false
		}
		return items, true
	}
	var page struct {
		Items   []DiscussionPost `json:"Items"`
		Objects []DiscussionPost `json:"Objects"`
	}
	if err := json.Unmarshal(raw, &page); err != nil {
		return nil, false
	}
	if page.Items != nil {
		return page.Items, true
	}
	return page.Objects, true
}

func renderPost(p DiscussionPost) string {
	if strings.TrimSpace(p.Subject) == "" && p.Message.Plain() == "" {
		raw, _ := json.MarshalIndent(p, "", "  ")
		return string(raw) + "\n"
	}
	var b strings.Builder
	indent := strings.Repeat("  ", p.Depth)
	author := p.Author.Label()
	head := p.Subject
	if head == "" {
		head = "(no subject)"
	}
	meta := render.When(p.PostingDate)
	if author != "" {
		meta += "  ·  " + author
	}
	if p.NumReplies > 0 {
		meta += "  ·  " + render.Count(p.NumReplies, "reply")
	}
	if !p.IsRead {
		meta += "  ·  " + render.Accent("unread")
	}
	b.WriteString(indent + render.Accent(head) + "\n")
	b.WriteString(indent + render.Muted(meta) + "\n")
	if body := p.Message.Plain(); body != "" {
		for _, line := range strings.Split(render.Wrap(body, render.Width()-len(indent)-4), "\n") {
			b.WriteString(indent + "    " + line + "\n")
		}
	}
	b.WriteString("\n")
	return b.String()
}
