// Rare per-course surfaces from plan §5.8 / §5.9 / §5.10. Each command
// takes `--course <orgUnitId>` to scope the request to one course.
package cmd

import (
	"encoding/json"
	"fmt"
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
	newsCourse  string
	newsEnvFile string
	newsAutoRef bool
	newsJSON    bool
	newsSince   string
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

func init() {
	root := &cobra.Command{
		Use:   "news",
		Short: "Course announcements and cross-course news (rare)",
		Long: "Course announcements. Usually read in the D2L web UI; this\n" +
			"command exists for completeness and for archival workflows.\n\n" +
			"`news --course <id>` lists per-course news. `news get <newsId>`\n" +
			"shows one item. `news attachment <newsId> <fileId>` downloads an\n" +
			"attachment. `news` with no `--course` returns cross-course news.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsList() },
	}
	root.Flags().StringVar(&newsCourse, "course", "", "Course OrgUnitId to scope to (omit for cross-course)")
	root.Flags().StringVarP(&newsEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	root.Flags().BoolVar(&newsAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
	root.Flags().BoolVar(&newsJSON, "json", false, "Emit raw JSON instead of a table")
	root.Flags().StringVar(&newsSince, "since", "", "ISO 8601 lower bound for cross-course news")

	get := &cobra.Command{
		Use: "get <newsId>", Short: "Show a single news item", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsGet(args[0]) },
	}
	get.Flags().StringVar(&newsCourse, "course", "", "Course OrgUnitId (required)")
	get.Flags().BoolVar(&newsJSON, "json", false, "Emit raw JSON")
	get.Flags().StringVarP(&newsEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	get.Flags().BoolVar(&newsAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")

	att := &cobra.Command{
		Use: "attachment <newsId> <fileId>", Short: "Download a news attachment", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runNewsAttachment(args[0], args[1]) },
	}
	att.Flags().StringVar(&newsCourse, "course", "", "Course OrgUnitId (required)")
	att.Flags().StringVar(&asgOut, "out", defaultHome(), "Output directory")
	att.Flags().StringVar(&asgName, "name", "", "Override output filename")
	att.Flags().StringVarP(&newsEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	att.Flags().BoolVar(&newsAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")

	root.AddCommand(get, att)
	rootCmd.AddCommand(root)
}

func newsSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     newsEnvFile,
		AutoRefresh: newsAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func runNewsList() error {
	ens, err := newsSession()
	if err != nil {
		return err
	}
	if newsCourse == "" {
		// No cross-course aggregator endpoint is available on CBE (the
		// `/users/me/news/` route returns 403). Aggregate by hitting the
		// per-course news endpoint once per enrolled course and merging
		// the results, sorted newest-first.
		csv, err := dupdata.CourseOrgIDsCSV(ens.Jar)
		if err != nil {
			return err
		}
		courseIDs := strings.Split(csv, ",")
		type flat struct {
			news NewsItem
			org  int
		}
		var all []flat
		for _, idStr := range courseIDs {
			idStr = strings.TrimSpace(idStr)
			orgID, _ := strconv.Atoi(idStr)
			raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/", ua.D2LBase, dupdata.D2LVersion(), idStr)
			if newsSince != "" {
				var err error
				raw, err = dupdata.AppendQuery(raw, "since", newsSince)
				if err != nil {
					return err
				}
			}
			items, err := dupdata.FetchJSONList[NewsItem](raw, ens.Jar)
			if err != nil {
				// Per-course failures shouldn't kill the whole aggregate —
				// skip and continue. log to stderr only with -v.
				fmt.Fprintf(os.Stderr, "[news] skipping course %s: %v\n", idStr, err)
				continue
			}
			for _, n := range items {
				all = append(all, flat{news: n, org: orgID})
			}
		}
		// Sort newest first by StartDate (desc).
		sort.Slice(all, func(i, j int) bool {
			return all[i].news.StartDate > all[j].news.StartDate
		})
		if newsJSON {
			type outItem struct {
				NewsItem
				OrgUnitIdHint int `json:"OrgUnitId"`
			}
			out := make([]outItem, 0, len(all))
			for _, f := range all {
				out = append(out, outItem{NewsItem: f.news, OrgUnitIdHint: f.org})
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
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/", ua.D2LBase, dupdata.D2LVersion(), newsCourse)
	if newsSince != "" {
		var err error
		raw, err = dupdata.AppendQuery(raw, "since", newsSince)
		if err != nil {
			return err
		}
	}
	items, err := dupdata.FetchJSONList[NewsItem](raw, ens.Jar)
	if err != nil {
		return err
	}
	if newsJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No announcements in this course.")
		return nil
	}
fmt.Printf("%d announcement%s\n", len(items), plural(len(items)))
	for _, n := range items {
		when := n.StartDate
		if len(when) > 16 {
			when = when[:16]
		}
		flags := ""
		if n.IsPinned {
			flags += "[pinned] "
		}
		if n.IsPublished == false {
			flags += "[draft] "
		}
		fmt.Printf("  %s\t%s\t%s%s\n", n.Id.String(), when, flags, truncate(n.Title, 80))
	}
		return nil
}

func runNewsGet(newsID string) error {
	if newsCourse == "" {
		return fmt.Errorf("--course <orgUnitId> is required for `news get`")
	}
	ens, err := newsSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/news/%s",
		ua.D2LBase, dupdata.D2LVersion(), newsCourse, newsID)
	var out NewsItem
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func runNewsAttachment(newsID, fileID string) error {
	if newsCourse == "" {
		return fmt.Errorf("--course <orgUnitId> is required for `news attachment`")
	}
	ens, err := newsSession()
	if err != nil {
		return err
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
