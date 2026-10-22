package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"net/url"
	"strconv"
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

const defaultCoursesURL = ua.D2LBase +
	"/d2l/le/manageCourses/api/mycourses" +
	"?pageSize=20&sort=current&autoPinCourses=false" +
	"&orgUnitTypeId=3&promotePins=true&embedDepth=0&widgetId=287426"

type d2lCourse struct {
	OrgUnitId       json.Number `json:"OrgUnitId"`
	TypeId          int         `json:"TypeId"`
	Name            string      `json:"Name"`
	Code            string      `json:"Code"`
	IsActive        bool        `json:"IsActive"`
	StartDate       *string     `json:"StartDate"`
	EndDate         *string     `json:"EndDate"`
	SemesterName    string      `json:"SemesterName"`
	CanAccessCourse bool        `json:"CanAccessCourse"`
}

type d2lCoursesResponse struct {
	Courses []d2lCourse `json:"Courses"`
}

type UpdateCount struct {
	OrgUnitId                   string `json:"OrgUnitId"`
	UserId                      string `json:"UserId"`
	UnreadDiscussions           int    `json:"UnreadDiscussions"`
	UnapprovedDiscussions       int    `json:"UnapprovedDiscussions"`
	UnreadAssignmentFeedback    int    `json:"UnreadAssignmentFeedback"`
	UnattemptedQuizzes          int    `json:"UnattemptedQuizzes"`
	UnreadAssignmentSubmissions int    `json:"UnreadAssignmentSubmissions"`
	UngradedQuizzes             int    `json:"UngradedQuizzes"`
	PendingEnrollmentRequests   int    `json:"PendingEnrollmentRequests"`
}

func (u UpdateCount) Total() int {
	return u.UnreadDiscussions + u.UnapprovedDiscussions + u.UnreadAssignmentFeedback +
		u.UnattemptedQuizzes + u.UnreadAssignmentSubmissions + u.UngradedQuizzes
}

func (u UpdateCount) Summary() string {
	if u.Total() == 0 {
		return render.Muted("quiet")
	}
	var parts []string
	if u.UnattemptedQuizzes > 0 {
		parts = append(parts, render.Count(u.UnattemptedQuizzes, "quiz")+" to try")
	}
	if u.UnreadAssignmentFeedback > 0 {
		parts = append(parts, render.Count(u.UnreadAssignmentFeedback, "feedback")+" to read")
	}
	if u.UnreadDiscussions > 0 {
		parts = append(parts, render.Count(u.UnreadDiscussions, "unread post"))
	}
	if u.UnreadAssignmentSubmissions > 0 {
		parts = append(parts, render.Count(u.UnreadAssignmentSubmissions, "submission")+" seen")
	}
	if u.UngradedQuizzes > 0 {
		parts = append(parts, render.Count(u.UngradedQuizzes, "quiz")+" ungraded")
	}
	return strings.Join(parts, ", ")
}

var (
	coursesURL         string
	coursesJSON        bool
	coursesAll         bool
	coursesEnvFile     string
	coursesAutoRefresh bool
	coursesLimit       int
	coursesNoRelated   bool
)

var courseCmd = &cobra.Command{
	Use:   "course",
	Short: "Discover and inspect your D2L courses (alias: enrollment)",
	Long: "Discover what's enrolled. `course list` hits the manageCourses API and\n" +
		"adds what needs your attention per course. `course get <orgUnitId>`\n" +
		"renders one course as a report and pulls in its news, counts and\n" +
		"upcoming work; pass --no-related to fetch just the course record.\n\n" +
		"Students cannot read `/courses/<id>` on CBE (D2L research §3.2), so\n" +
		"`course get` falls back to your enrollment record instead of failing.\n\n" +
		"`enrollment` is an alias for `course` — both names work, matching D2L's\n" +
		"internal `enrollments/myenrollments` terminology.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var courseListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your D2L courses, with what needs your attention",
	RunE: func(cmd *cobra.Command, args []string) error {
		ens, err := session.EnsureSession(session.EnsureOptions{
			EnvFile:     coursesEnvFile,
			AutoRefresh: coursesAutoRefresh,
			KMSI:        true,
			LoginFn:     loginFn,
		})
		if err != nil {
			return err
		}

		var courses []d2lCourse
		if coursesAll {
			courses, err = fetchAllPages(ens.Jar, coursesURL)
			if err != nil {
				return err
			}
		} else {
			data, err := fetchCourses(ens.Jar, coursesURL)
			if err != nil {
				return err
			}
			courses = data.Courses
		}
		if coursesLimit > 0 && len(courses) > coursesLimit {
			courses = courses[:coursesLimit]
		}

		counts := map[string]UpdateCount{}
		if !coursesNoRelated {
			counts = fetchUpdateCounts(ens.Jar, courses)
		}

		if coursesJSON {
			type row struct {
				d2lCourse
				Updates *UpdateCount `json:"Updates,omitempty"`
			}
			out := make([]row, len(courses))
			for i, c := range courses {
				out[i].d2lCourse = c
				if u, ok := counts[c.OrgUnitId.String()]; ok {
					u := u
					out[i].Updates = &u
				}
			}
			return render.PrintJSON(map[string]any{"count": len(out), "courses": out})
		}
		printCourses(courses, counts)
		return nil
	},
}

var courseGetCmd = &cobra.Command{
	Use:   "get <orgUnitId>",
	Short: "Show one course as a report: term, dates, news and what needs you",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		ens, err := session.EnsureSession(session.EnsureOptions{
			EnvFile:     coursesEnvFile,
			AutoRefresh: coursesAutoRefresh,
			KMSI:        true,
			LoginFn:     loginFn,
		})
		if err != nil {
			return err
		}
		return runCourseGet(ens.Jar, args[0])
	},
}

func init() {
	courseListCmd.Flags().StringVarP(&coursesURL, "url", "u", defaultCoursesURL, "Override the my-courses API URL")
	courseListCmd.Flags().BoolVar(&coursesJSON, "json", false, "Emit a JSON envelope instead of a table")
	courseListCmd.Flags().BoolVar(&coursesAll, "all", false, "Fetch all pages, not just the first")
	courseListCmd.Flags().IntVar(&coursesLimit, "limit", 0, "Cap the number of items returned (0 = no cap)")
	courseListCmd.Flags().StringVarP(&coursesEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	courseListCmd.Flags().BoolVar(&coursesAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")
	courseListCmd.Flags().BoolVar(&coursesNoRelated, "no-related", false, "Skip the per-course activity lookups")

	courseGetCmd.Flags().BoolVar(&coursesJSON, "json", false, "Emit a JSON envelope instead of a report")
	courseGetCmd.Flags().BoolVar(&coursesNoRelated, "no-related", false, "Fetch only the course record")
	courseGetCmd.Flags().StringVarP(&coursesEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	courseGetCmd.Flags().BoolVar(&coursesAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")

	courseCmd.AddCommand(courseListCmd)
	courseCmd.AddCommand(courseGetCmd)
	rootCmd.AddCommand(courseCmd)

	enrollmentCmd := *courseCmd
	enrollmentCmd.Use = "enrollment"
	enrollmentCmd.Short = "Alias for `course` (matches D2L's enrollments/myenrollments terminology)"
	enrollmentCmd.Long = "Alias for the `course` command. Both names work and are interchangeable.\n\n" +
		"`enrollment` is preferred when the mental model is " +
		"\"what am I enrolled in\"; `course` is preferred when the model is " +
		"\"what's the per-course detail\"."
	rootCmd.AddCommand(&enrollmentCmd)
}

func fetchUpdateCounts(jar *cookiejar.Jar, courses []d2lCourse) map[string]UpdateCount {
	out := map[string]UpdateCount{}
	if len(courses) == 0 {
		return out
	}
	ids := make([]string, len(courses))
	for i, c := range courses {
		ids[i] = c.OrgUnitId.String()
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/updates/myUpdates/?orgUnitIdsCSV=%s",
		ua.D2LBase, dupdata.D2LVersion(), strings.Join(ids, ","))
	items, err := dupdata.FetchJSONList[UpdateCount](raw, jar)
	if err != nil {
		return out
	}
	for _, u := range items {
		out[u.OrgUnitId] = u
	}
	return out
}

func courseReport(jar *cookiejar.Jar, id string) (map[string]any, []string) {
	notes := []string{}
	raw := ua.D2LBase + "/d2l/api/lp/" + dupdata.LPVersion() + "/courses/" + id
	var out map[string]any
	if err := dupdata.FetchJSON(raw, jar, &out); err != nil {
		if strings.Contains(err.Error(), " 403 ") {
			notes = append(notes, "Students cannot read /courses/<id> on CBE, so this report is built from your enrollment record.")
			out = map[string]any{}
		} else {
			notes = append(notes, err.Error())
			out = map[string]any{}
		}
	}
	return out, notes
}

type courseReportOut struct {
	Course  map[string]any `json:"course"`
	Updates *UpdateCount   `json:"updates,omitempty"`
	News    []NewsItem     `json:"news"`
	Notes   []string       `json:"notes"`
	Related map[string]int `json:"related,omitempty"`
}

func runCourseGet(jar *cookiejar.Jar, id string) error {
	detail, notes := courseReport(jar, id)

	enrolled := map[string]d2lCourse{}
	if all, err := fetchAllPages(jar, coursesURL); err == nil {
		for _, c := range all {
			enrolled[c.OrgUnitId.String()] = c
		}
	}
	own, haveOwn := enrolled[id]

	rep := courseReportOut{Course: detail, News: []NewsItem{}, Notes: notes}

	if !coursesNoRelated {
		counts := fetchUpdateCounts(jar, []d2lCourse{own})
		if u, ok := counts[id]; ok {
			rep.Updates = &u
		}
		if items, err := dupdata.FetchJSONList[NewsItem](
			fmt.Sprintf("%s/d2l/api/le/%s/%s/news/", ua.D2LBase, dupdata.D2LVersion(), id), jar); err == nil {
			rep.News = items
		}

		results := fetchAll(3, func(i int) (int, error) {
			switch i {
			case 0:
				fs, err := dupdata.FetchJSONList[AssignmentFolder](
					asgURL(id, "folders/?onlyCurrentStudentsAndGroups=true"), jar)
				return len(fs), err
			case 1:
				qs, err := dupdata.FetchJSONList[Quiz](quizURL(id, ""), jar)
				return len(qs), err
			default:
				gs, err := dupdata.FetchJSONList[GradeValue](
					fmt.Sprintf("%s/d2l/api/le/%s/%s/grades/values/myGradeValues/", ua.D2LBase, dupdata.D2LVersion(), id), jar)
				return len(gs), err
			}
		})
		labels := []string{"assignments", "quizzes", "grades"}
		rel := map[string]int{}
		for i, r := range results {
			if r.err == nil {
				rel[labels[i]] = r.value
			}
		}
		rep.Related = rel
	}

	if coursesJSON {
		return render.PrintJSON(rep)
	}
	fmt.Print(renderCourseReport(rep, id, own, haveOwn))
	return nil
}

func renderCourseReport(rep courseReportOut, id string, own d2lCourse, haveOwn bool) string {
	var b strings.Builder

	name := firstString(rep.Course, "Name", "FullName")
	if name == "" && haveOwn {
		name = own.Name
	}
	if name == "" {
		name = "Course " + id
	}
	b.WriteString(render.Title(name))

	code := firstString(rep.Course, "Code")
	term := firstString(rep.Course, "SemesterName")
	if code == "" && haveOwn {
		code = own.Code
	}
	if term == "" && haveOwn {
		term = own.SemesterName
	}

	rows := []render.Row{{Key: "Course", Value: id}}
	if code != "" {
		rows = append(rows, render.Row{Key: "Code", Value: code})
	}
	if term != "" {
		rows = append(rows, render.Row{Key: "Term", Value: term})
	}

	start, end := "", ""
	if s, ok := rep.Course["StartDate"].(string); ok {
		start = s
	}
	if e, ok := rep.Course["EndDate"].(string); ok {
		end = e
	}
	if start == "" && haveOwn && own.StartDate != nil {
		start = *own.StartDate
	}
	if end == "" && haveOwn && own.EndDate != nil {
		end = *own.EndDate
	}
	if start != "" || end != "" {
		rows = append(rows, render.Row{Key: "Runs", Value: render.Window(start, end)})
	}

	if haveOwn {
		state := render.Muted("inactive")
		if own.IsActive {
			state = render.OK("active")
		}
		rows = append(rows, render.Row{Key: "Status", Value: state})
		rows = append(rows, render.Row{Key: "Access", Value: accessWord(own.CanAccessCourse)})
	}
	b.WriteString(render.KV(rows...))

	if rep.Updates != nil && rep.Updates.Total() > 0 {
		b.WriteString(render.Section("Needs you"))
		b.WriteString(render.Bullets(
			summaryBullets(*rep.Updates)...,
		))
	}

	if len(rep.Related) > 0 {
		b.WriteString(render.Section("At a glance"))
		b.WriteString(render.KV(
			render.Row{Key: "Assignments", Value: render.Count(rep.Related["assignments"], "folder")},
			render.Row{Key: "Quizzes", Value: render.Count(rep.Related["quizzes"], "quiz")},
			render.Row{Key: "Grades", Value: render.Count(rep.Related["grades"], "grade item")},
			render.Row{Key: "News", Value: render.Count(len(rep.News), "post")},
		))
	}

	if len(rep.News) > 0 {
		b.WriteString(render.Section("Latest news"))
		for i, n := range rep.News {
			if i >= 3 {
				break
			}
			when := n.Date()
			b.WriteString(fmt.Sprintf("  %s  %s\n", render.Muted(render.Truncate(render.Rel(when), 12)), render.Truncate(n.Title, 60)))
		}
		b.WriteString(render.Hint("schooltools news --course %s", id))
	}

	if len(rep.Notes) > 0 {
		b.WriteString(render.Section("Notes"))
		for _, n := range rep.Notes {
			for _, line := range strings.Split(render.Wrap(n, render.Width()-2), "\n") {
				b.WriteString(render.Note("%s", line))
			}
		}
	}

	b.WriteString(render.Section("Next"))
	b.WriteString(render.Hint("schooltools content --course %s", id))
	if rep.Related["assignments"] > 0 {
		b.WriteString(render.Hint("schooltools assignment list --course %s", id))
	}
	if rep.Related["quizzes"] > 0 {
		b.WriteString(render.Hint("schooltools quiz list --course %s", id))
	}
	return b.String()
}

func summaryBullets(u UpdateCount) []string {
	var out []string
	if u.UnattemptedQuizzes > 0 {
		out = append(out, render.Count(u.UnattemptedQuizzes, "quiz")+" not attempted")
	}
	if u.UnreadAssignmentFeedback > 0 {
		out = append(out, render.Count(u.UnreadAssignmentFeedback, "feedback")+" waiting to be read")
	}
	if u.UnreadDiscussions > 0 {
		out = append(out, render.Count(u.UnreadDiscussions, "unread discussion post"))
	}
	if u.UnreadAssignmentSubmissions > 0 {
		out = append(out, render.Count(u.UnreadAssignmentSubmissions, "unread submission"))
	}
	if u.UngradedQuizzes > 0 {
		out = append(out, render.Count(u.UngradedQuizzes, "quiz")+" awaiting a grade")
	}
	return out
}

func accessWord(ok bool) string {
	if ok {
		return render.OK("available")
	}
	return render.Bad("not accessible")
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func fetchCourses(jar *cookiejar.Jar, rawURL string) (d2lCoursesResponse, error) {
	res, err := httpclient.FollowRedirects(rawURL, jar, httpclient.FetchOptions{})
	if err != nil {
		return d2lCoursesResponse{}, err
	}
	if res.StatusCode != 200 {
		if ua.IsLoginURL(res.Request.URL.String()) {
			_ = session.Clear()
			return d2lCoursesResponse{}, fmt.Errorf("session expired (landed on %s); run 'schooltools login' again", res.Request.URL.String())
		}
		return d2lCoursesResponse{}, fmt.Errorf("D2L my-courses API returned %d %s", res.StatusCode, res.Status)
	}
	ct := res.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") && !strings.Contains(ct, "text/json") {
		return d2lCoursesResponse{}, fmt.Errorf("expected JSON, got %s from %s", ct, res.Request.URL.String())
	}
	body, err := httpclient.BodyBytes(res)
	if err != nil {
		return d2lCoursesResponse{}, err
	}
	var out d2lCoursesResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return d2lCoursesResponse{}, err
	}
	return out, nil
}

func fetchAllPages(jar *cookiejar.Jar, firstURL string) ([]d2lCourse, error) {
	var all []d2lCourse
	next := firstURL
	pageNum := 0
	for next != "" {
		pageNum++
		data, err := fetchCourses(jar, next)
		if err != nil {
			return all, err
		}
		page := data.Courses
		all = append(all, page...)
		if len(page) < 20 {
			break
		}
		next = advancePage(next, pageNum+1)
	}
	return all, nil
}

func advancePage(rawURL string, page int) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	q := u.Query()
	q.Set("page", strconv.Itoa(page))
	u.RawQuery = q.Encode()
	return u.String()
}

func printCourses(courses []d2lCourse, counts map[string]UpdateCount) {
	if len(courses) == 0 {
		fmt.Print(render.Empty("No courses found."))
		return
	}
	fmt.Print(render.Title("Your courses"))
	rows := make([][]string, len(courses))
	for i, c := range courses {
		activity := render.Muted("quiet")
		if u, ok := counts[c.OrgUnitId.String()]; ok {
			activity = u.Summary()
		}
		rows[i] = []string{
			c.OrgUnitId.String(),
			render.StatusWord(courseState(c)),
			render.Truncate(c.SemesterName, 22),
			activity,
			render.Truncate(c.Code, 26),
			c.Name,
		}
	}
	fmt.Print(table.Render(table.Options{
		Headers: []string{"ID", "Status", "Term", "Activity", "Code", "Name"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(9), table.Fixed(22), table.Flex(22), table.Fixed(26), table.Flex(28)}, table.TerminalWidth()),
		Rows:    rows,
	}))
	fmt.Print(render.Hint("schooltools course get <id>   for one course's full report"))
}

func courseState(c d2lCourse) string {
	if !c.IsActive {
		return "inactive"
	}
	if t, ok := render.Parse(dateOrEmpty(c.EndDate)); ok && time.Now().After(t) {
		return "complete"
	}
	return "active"
}

func dateOrEmpty(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func formatDate(s *string) string {
	return render.Date(dateOrEmpty(s))
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
