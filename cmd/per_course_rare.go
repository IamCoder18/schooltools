// Rare per-course surfaces from plan §5.8 / §5.9 / §5.10. Each command
// takes `--course <orgUnitId>` to scope the request to one course.
package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
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
	Id            json.Number `json:"Id"`
	OrgUnitId     int         `json:"OrgUnitId,omitempty"`
	Title         string      `json:"Title"`
	Body          any         `json:"Body"`
	StartDate     string      `json:"StartDate"`
	EndDate       string      `json:"EndDate,omitempty"`
	IsPinned      bool        `json:"IsPinned"`
	IsGlobal      bool        `json:"IsGlobal,omitempty"`
	HasAttachment bool        `json:"HasAttachment"`

	// Fields added at LE 1.97+ — surfaced when the tenant is on a new
	// enough version. Older versions just leave them at the zero value.
	PinnedDate             *string `json:"PinnedDate"`
	IsPublished            bool    `json:"IsPublished"`
	IsStartDateShown       bool    `json:"IsStartDateShown"`
	IsAuthorInfoShown      bool    `json:"IsAuthorInfoShown"`
	ShowOnlyInCourseOfferings bool `json:"ShowOnlyInCourseOfferings"`
	SortOrder              int     `json:"SortOrder"`
	Attachments            []any   `json:"Attachments,omitempty"`
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
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsList() },
	}
	// Inherit every persistent flag from the parent `news` command so
	// `news list --course X --since Y --json` works without re-registering.
	list.PersistentFlags().AddFlagSet(root.PersistentFlags())

	get := &cobra.Command{
		Use: "get <newsId>", Short: "Show a single news item", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsGet(args[0]) },
	}
	get.PersistentFlags().AddFlagSet(root.PersistentFlags())
	get.Flags().BoolVar(&newsCrossTry, "cross-try", true, "When --course is omitted, try every enrolled course and return the first hit")
	get.Flags().StringVar(&newsBodyFmt, "body-format", "text", "When --json: text|html|both (flatten Body accordingly)")

	att := &cobra.Command{
		Use: "attachment <newsId> <fileId>", Short: "Download a news attachment", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsAttachment(args[0], args[1]) },
	}
	att.PersistentFlags().AddFlagSet(root.PersistentFlags())
	att.Flags().BoolVar(&newsCrossTry, "cross-try", true, "When --course is omitted, try every enrolled course")
	att.Flags().StringVar(&asgOut, "out", defaultHome(), "Output directory")
	att.Flags().StringVar(&asgName, "name", "", "Override output filename")

	root.AddCommand(list, get, att)
	rootCmd.AddCommand(root)
}

// registerNewsFlags is the common flag set for both `news` and `news list`.
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
		return b.Html, ""
	}
}

func runNewsList() error {
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
		fmt.Println("No announcements in this course.")
		return nil
	}
	fmt.Printf("%d announcement%s in course %s\n", len(items), plural(len(items)), courseID)
	for _, n := range items {
		when := n.StartDate
		if len(when) > 16 {
			when = when[:16]
		}
		flags := ""
		if n.IsPinned {
			flags += "[pinned] "
		}
		if !n.IsPublished {
			flags += "[draft] "
		}
		fmt.Printf("  %s\t%s\t%s%s\n", n.Id.String(), when, flags, truncate(n.Title, 80))
	}
	return nil
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
		for _, n := range items {
			all = append(all, flat{news: n, org: orgID})
		}
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
		fmt.Println("No announcements across any of your courses.")
		return nil
	}
	fmt.Printf("%d announcement%s across %d course%s\n",
		len(all), plural(len(all)), len(courseIDs), plural(len(courseIDs)))
	for _, f := range all {
		when := f.news.StartDate
		if len(when) > 16 {
			when = when[:16]
		}
		pin := ""
		if f.news.IsPinned {
			pin = "[pinned] "
		}
		fmt.Printf("  %s\t%s\torg=%d\t%s%s\n", f.news.Id.String(), when, f.org, pin, truncate(f.news.Title, 80))
	}
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
	return []newsListJSONItem{{
		Id:            n.Id.String(),
		OrgUnitId:     n.OrgUnitId,
		Title:         n.Title,
		Body:          text,
		BodyHtml:      html,
		StartDate:     n.StartDate,
		EndDate:       n.EndDate,
		IsPinned:      n.IsPinned,
		IsPublished:   n.IsPublished,
		HasAttachment: n.HasAttachment,
		Source:        sourceCourse,
	}}
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
		out := map[string]any{
			"id":            n.Id.String(),
			"orgUnitId":     n.OrgUnitId,
			"sourceCourse":  sourceCourse,
			"title":         n.Title,
			"body":          text,
			"bodyHtml":      html,
			"startDate":     n.StartDate,
			"endDate":       n.EndDate,
			"isPinned":      n.IsPinned,
			"isPublished":   n.IsPublished,
			"hasAttachment": n.HasAttachment,
			"attachments":   n.Attachments,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	b, _ := json.MarshalIndent(n, "", "  ")
	fmt.Println(string(b))
	return nil
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
	GradeObjectId       int     `json:"GradeObjectId"`
	GradeObjectName     string  `json:"GradeObjectName"`
	GradeObjectType     int     `json:"GradeObjectType"`
	PointsNumerator     float64 `json:"PointsNumerator"`
	PointsDenominator   float64 `json:"PointsDenominator"`
	DisplayedGrade      string  `json:"DisplayedGrade"`
	GradeSchemeSymbol   string  `json:"GradeSchemeSymbol"`
	IsReleased          bool    `json:"IsReleased"`
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
			b, _ := json.MarshalIndent(items, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		if len(items) == 0 {
			fmt.Println("No course finals released yet.")
			return nil
		}
		fmt.Printf("%d course final%s\n", len(items), plural(len(items)))
		for _, g := range items {
			score := g.DisplayedGrade
			if score == "" {
				score = fmt.Sprintf("%.2f / %.2f", g.PointsNumerator, g.PointsDenominator)
			}
			fmt.Printf("  %d\t%s\t%s\n", g.GradeObjectId, score, g.GradeObjectName)
		}
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
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Printf("Course %s final: %s (%s)\n", gradeCourse, out.DisplayedGrade, out.GradeSchemeSymbol)
		return nil
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/grades/values/myGradeValues/",
		ua.D2LBase, dupdata.D2LVersion(), gradeCourse)
	items, err := dupdata.FetchJSONList[GradeValue](raw, ens.Jar)
	if err != nil {
		return err
	}
	if gradeJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No grades found for this course.")
		return nil
	}
	fmt.Printf("%d grade item%s in course %s\n", len(items), plural(len(items)), gradeCourse)
	for _, g := range items {
		score := fmt.Sprintf("%.2f / %.2f", g.PointsNumerator, g.PointsDenominator)
		if g.DisplayedGrade != "" {
			score = g.DisplayedGrade
		}
		fmt.Printf("  %d\t%s\t%s\n", g.GradeObjectId, score, g.GradeObjectName)
	}
	return nil
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
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
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
	Id            json.Number `json:"Id"`
	Name          string      `json:"Name"`
	Description   any         `json:"Description"`
	AllowAnon     bool        `json:"AllowAnonymous"`
	IsHidden      bool        `json:"IsHidden"`
	NumTopics     int         `json:"NumTopics"`
	NumPosts      int         `json:"NumPosts"`
	LastPostDate  string      `json:"LastPostDate"`
}

type ForumTopic struct {
	Id            int    `json:"Id"`
	Name          string `json:"Name"`
	NumPosts      int    `json:"NumPosts"`
	LastPostDate  string `json:"LastPostDate"`
	IsPinned      bool   `json:"IsPinned"`
	IsLocked      bool   `json:"IsLocked"`
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No forums found for this course.")
		return nil
	}
	fmt.Printf("%d forum%s\n", len(items), plural(len(items)))
	for _, f := range items {
		fmt.Printf("  %s\t%d topic%s, %d post%s\t%s\n",
			f.Id.String(), f.NumTopics, plural(f.NumTopics), f.NumPosts, plural(f.NumPosts), f.Name)
	}
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
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No topics found in this forum.")
		return nil
	}
	fmt.Printf("%d topic%s\n", len(items), plural(len(items)))
	for _, t := range items {
		pin := ""
		if t.IsPinned {
			pin = "[pinned] "
		}
		fmt.Printf("  %d\t%s%s\n", t.Id, pin, truncate(t.Name, 80))
	}
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
		// Try the alternate per-topic form.
		raw2 := fmt.Sprintf("%s/d2l/api/le/%s/%s/discussions/topics/%s/posts/?pageSize=20&pageNumber=1",
			ua.D2LBase, dupdata.D2LVersion(), courseID, topicID)
		if err2 := dupdata.FetchJSON(raw2, ens.Jar, &out); err2 != nil {
			return err
		}
	}
	fmt.Println(string(out))
	return nil
}
