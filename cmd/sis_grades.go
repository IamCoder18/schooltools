package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/ps"
	"github.com/IamCoder18/schooltools/internal/table"
)

var (
	sisGradesYear  int
	sisGradesMarks bool
)

// sisSectionRow is the flattened JSON shape for one enrolled class.
// PowerSchool nests the course details under _coursesectioninfo; we
// flatten it so every sis JSON payload uses snake_case and one level of
// nesting.
type sisSectionRow struct {
	SectionDCID  int64  `json:"section_dcid"`
	Status       string `json:"status"`
	EnrolledLate bool   `json:"enrolled_late"`
	SectionNum   string `json:"section_num,omitempty"`
	Expression   string `json:"expression,omitempty"`
	CourseName   string `json:"course_name"`
	CourseNum    string `json:"course_num"`
	Teacher      string `json:"teacher"`
	Term         string `json:"term"`
	TermAbbr     string `json:"term_abbr"`
}

func sisSectionRows(enrollments []ps.Enrollment) []sisSectionRow {
	out := make([]sisSectionRow, 0, len(enrollments))
	for _, e := range enrollments {
		out = append(out, sisSectionRow{
			SectionDCID:  e.SectionDCID,
			Status:       e.Status,
			EnrolledLate: e.EnrolledLate,
			SectionNum:   e.Section.SectionNum,
			Expression:   e.Section.Expression,
			CourseName:   e.Section.CourseName,
			CourseNum:    e.Section.CourseNum,
			Teacher:      e.Section.TeacherName,
			Term:         e.Section.TermName,
			TermAbbr:     e.Section.TermAbbreviation,
		})
	}
	return out
}

var sisGradesCmd = &cobra.Command{
	Use:   "grades",
	Short: "List the student's enrolled classes, or their current course marks",
	Long: "By default lists every class the student is enrolled in for the\n" +
		"active school year. With --marks, reads the portal's\n" +
		"\"Assignments & Grades\" page and prints the current T1/S1 mark\n" +
		"for each course, matching what PowerSchool displays.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")

		client, err := sisClient(cmd)
		if err != nil {
			return err
		}
		if sisGradesMarks {
			return runSISGradesMarks(client, asJSON)
		}
		dcid, err := resolveStudentDCID(client)
		if err != nil {
			return err
		}
		yearID, err := resolveYearID(client, dcid, sisGradesYear)
		if err != nil {
			return err
		}

		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return err
		}
		rows := sisSectionRows(stu.Enrollments)

		if asJSON {
			out := map[string]any{
				"count":    len(rows),
				"year_id":  yearID,
				"student":  stu.LastFirst,
				"sections": rows,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d section(s) for %s (year %d)\n", len(rows), stu.LastFirst, yearID)
		tableRows := make([][]string, len(rows))
		for i, r := range rows {
			tableRows[i] = []string{
				fmt.Sprintf("%d", r.SectionDCID),
				r.TermAbbr,
				r.CourseNum,
				r.CourseName,
				r.Teacher,
				r.Term,
			}
		}
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Section", "Term", "Code", "Course", "Teacher", "Term Name"},
			Widths: table.Widths(
				[]table.ColumnSpec{
					table.Fixed(10), table.Fixed(6), table.Fixed(14),
					table.Flex(16), table.Flex(16), table.Flex(12),
				},
				table.TerminalWidth()),
			Rows: tableRows,
			Wrap: true,
		}))
		return nil
	},
}

func runSISGradesMarks(client *ps.Client, asJSON bool) error {
	marks, err := client.GetCourseMarks()
	if err != nil {
		return err
	}
	if asJSON {
		out := map[string]any{"count": len(marks), "courses": marks}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(marks) == 0 {
		fmt.Fprintln(os.Stderr, "No course marks posted yet.")
		return nil
	}
	terms := ps.MarkTerms()
	headers := []string{"Course", "Teacher"}
	headers = append(headers, terms...)
	rows := make([][]string, len(marks))
	for i, m := range marks {
		row := []string{m.Course, m.Teacher}
		for _, term := range terms {
			row = append(row, markOrDash(m.MarkFor(term)))
		}
		rows[i] = row
	}
	widths := []table.ColumnSpec{table.Flex(16), table.Flex(12)}
	for range terms {
		widths = append(widths, table.Fixed(6))
	}
	fmt.Print(table.Render(table.Options{
		Headers: headers,
		Widths:  table.Widths(widths, table.TerminalWidth()),
		Rows:    rows,
		Wrap:    true,
	}))
	return nil
}

func markOrDash(mark string) string {
	if mark == "" {
		return "—"
	}
	return mark
}

func init() {
	sisGradesCmd.Flags().IntVar(&sisGradesYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisGradesCmd.Flags().BoolVar(&sisGradesMarks, "marks", false, "Show current course marks (T1/S1) from the guardian grades page")
	sisCmd.AddCommand(sisGradesCmd)
}
