package cmd

import (
	"encoding/json"
	"fmt"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/httpclient"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/ua"
)

const defaultCoursesURL = ua.D2LBase +
	"/d2l/le/manageCourses/api/mycourses" +
	"?pageSize=20&sort=current&autoPinCourses=false" +
	"&orgUnitTypeId=3&promotePins=true&embedDepth=0&widgetId=287426"

type d2lCourse struct {
	OrgUnitId        json.Number `json:"OrgUnitId"`
	TypeId           int         `json:"TypeId"`
	Name             string      `json:"Name"`
	Code             string      `json:"Code"`
	IsActive         bool        `json:"IsActive"`
	StartDate        *string     `json:"StartDate"`
	EndDate          *string     `json:"EndDate"`
	SemesterName     string      `json:"SemesterName"`
	CanAccessCourse  bool        `json:"CanAccessCourse"`
}

type d2lCoursesResponse struct {
	Courses []d2lCourse `json:"Courses"`
}

var (
	coursesURL         string
	coursesJSON        bool
	coursesAll         bool
	coursesEnvFile     string
	coursesAutoRefresh bool
	coursesLimit       int
)

var courseCmd = &cobra.Command{
	Use:   "course",
	Short: "Discover and inspect your D2L courses (alias: enrollment)",
	Long: "Discover what's enrolled. The `course list` subcommand hits the\n" +
		"manageCourses API; `course get <orgUnitId>` fetches a single course's\n" +
		"detail via the Learning Platform API.\n\n" +
		"`enrollment` is an alias for `course` — both names work, matching D2L's\n" +
		"internal `enrollments/myenrollments` terminology.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return cmd.Help()
	},
}

var courseListCmd = &cobra.Command{
	Use:   "list",
	Short: "List your D2L courses via the manageCourses API",
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

		if coursesAll {
			courses, err := fetchAllPages(ens.Jar, coursesURL)
			if err != nil {
				return err
			}
			if coursesLimit > 0 && len(courses) > coursesLimit {
				courses = courses[:coursesLimit]
			}
			if coursesJSON {
				out, _ := json.MarshalIndent(map[string]any{"Courses": courses}, "", "  ")
				fmt.Println(string(out))
				return nil
			}
			printCourses(courses)
			return nil
		}

		data, err := fetchCourses(ens.Jar, coursesURL)
		if err != nil {
			return err
		}
		courses := data.Courses
		if coursesLimit > 0 && len(courses) > coursesLimit {
			courses = courses[:coursesLimit]
		}
		if coursesJSON {
			resp := d2lCoursesResponse{Courses: courses}
			out, _ := json.MarshalIndent(resp, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		printCourses(courses)
		return nil
	},
}

var courseGetCmd = &cobra.Command{
	Use:   "get <orgUnitId>",
	Short: "Fetch a single course's detail via the Learning Platform API",
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
		raw := ua.D2LBase + "/d2l/api/lp/" + dupdata.D2LVersion() + "/courses/" + args[0]
		var out map[string]any
		if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
			// 403 for student role on CBE (per D2L research §3.2) — print a
			// clear "not authorised" message rather than a raw error.
			if strings.Contains(err.Error(), " 403 ") {
				return fmt.Errorf("not authorised: students cannot read /courses/<id> on CBE; use `course list` for the courses you're enrolled in")
			}
			return err
		}
		if coursesJSON {
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	},
}

func init() {
	courseListCmd.Flags().StringVarP(&coursesURL, "url", "u", defaultCoursesURL, "Override the my-courses API URL")
	courseListCmd.Flags().BoolVar(&coursesJSON, "json", false, "Emit raw JSON instead of a table")
	courseListCmd.Flags().BoolVar(&coursesAll, "all", false, "Fetch all pages, not just the first")
	courseListCmd.Flags().IntVar(&coursesLimit, "limit", 0, "Cap the number of items returned (0 = no cap)")
	courseListCmd.Flags().StringVarP(&coursesEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	courseListCmd.Flags().BoolVar(&coursesAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")

	courseGetCmd.Flags().BoolVar(&coursesJSON, "json", false, "Emit raw JSON instead of pretty-printed")
	courseGetCmd.Flags().StringVarP(&coursesEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	courseGetCmd.Flags().BoolVar(&coursesAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")

	courseCmd.AddCommand(courseListCmd)
	courseCmd.AddCommand(courseGetCmd)
	rootCmd.AddCommand(courseCmd)

	// `enrollment` is an alias for `course` per plan §2.1 / §9.7.
	enrollmentCmd := *courseCmd
	enrollmentCmd.Use = "enrollment"
	enrollmentCmd.Short = "Alias for `course` (matches D2L's enrollments/myenrollments terminology)"
	enrollmentCmd.Long = "Alias for the `course` command. Both names work and are interchangeable.\n\n" +
		"`enrollment` is preferred when the mental model is " +
		"\"what am I enrolled in\"; `course` is preferred when the model is " +
		"\"what's the per-course detail\"."
	rootCmd.AddCommand(&enrollmentCmd)
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
		fmt.Fprintf(os.Stderr, "[courses] page %d: +%d (running total %d)\n", pageNum, len(page), len(all))
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

func formatDate(s *string) string {
	if s == nil || *s == "" {
		return "—"
	}
	out := *s
	if len(out) >= 10 {
		return out[:10]
	}
	return out
}

func activeMark(active bool) string {
	if !active {
		return "○ no"
	}
	return "● yes"
}

func printCourses(courses []d2lCourse) {
	if len(courses) == 0 {
		fmt.Println("No courses found.")
		return
	}
	fmt.Printf("Found %d course%s\n", len(courses), plural(len(courses)))
	rows := make([][]string, len(courses))
	for i, c := range courses {
		rows[i] = []string{
			c.OrgUnitId.String(),
			activeMark(c.IsActive),
			formatDate(c.StartDate),
			formatDate(c.EndDate),
			c.SemesterName,
			c.Code,
			c.Name,
		}
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Active", "Start", "End", "Semester", "Code", "Name"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(6), table.Fixed(10), table.Fixed(10), table.Fixed(22), table.Flex(20), table.Flex(24)}, table.TerminalWidth()),
		Rows:    rows,
	})
	fmt.Print(out)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
