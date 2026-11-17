package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/ps"
	"github.com/IamCoder18/schooltools/internal/table"
)

var (
	sisStandardGradesYear    int
	sisStandardGradesSection int64
)

// sisStandardGradeRow is the flattened JSON shape for one outcome grade.
type sisStandardGradeRow struct {
	StandardGradeID int64    `json:"standard_grade_id"`
	StudentDCID     int64    `json:"student_dcid"`
	StandardID      int64    `json:"standard_id"`
	SectionDCID     int64    `json:"section_dcid"`
	StoreCode       string   `json:"store_code"`
	Course          string   `json:"course"`
	Grade           string   `json:"grade"`
	Percent         *float64 `json:"percent"`
	Flags           []string `json:"flags,omitempty"`
	LastUpdated     string   `json:"last_updated"`
}

func sisStandardGradeRows(rows []ps.StandardGrade, sectionName map[int64]string) []sisStandardGradeRow {
	out := make([]sisStandardGradeRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, sisStandardGradeRow{
			StandardGradeID: r.ID,
			StudentDCID:     r.StudentDCID,
			StandardID:      r.StandardID,
			SectionDCID:     r.SectionDCID,
			StoreCode:       r.StoreCode,
			Course:          sectionName[r.SectionDCID],
			Grade:           r.Grade,
			Percent:         r.Percent,
			Flags:           standardGradeFlags(r),
			LastUpdated:     r.LastUpdated,
		})
	}
	return out
}

func standardGradeFlags(r ps.StandardGrade) []string {
	var flags []string
	if r.IsExempt {
		flags = append(flags, "exempt")
	}
	if r.IsIncomplete {
		flags = append(flags, "incomplete")
	}
	if r.IsLocked {
		flags = append(flags, "locked")
	}
	if !r.IsEnrolled {
		flags = append(flags, "not_enrolled")
	}
	if r.IsMigrated {
		flags = append(flags, "migrated")
	}
	if r.IsAdminModified {
		flags = append(flags, "admin_modified")
	}
	if r.IsReportsRollupExcluded {
		flags = append(flags, "reports_rollup_excluded")
	}
	if r.IsManuallyOverridden {
		flags = append(flags, "manually_overridden")
	}
	return flags
}

func formatPercent(p *float64) string {
	if p == nil {
		return "—"
	}
	return strconv.FormatFloat(*p, 'f', -1, 64)
}

var sisStandardGradesCmd = &cobra.Command{
	Use:   "standard-grades",
	Short: "Show (student, section, standard) grade rows posted by teachers",
	Long: "Lists every standard-grade row PowerSchool has for the active\n" +
		"school year, including the proficiency code and the calculated\n" +
		"percentage. Each row also carries per-(student, section, standard)\n" +
		"flags (isexempt, isincomplete, islocked, isenrolled, …); the\n" +
		"per-assignment score behind the outcome is available from\n" +
		"`sis assignments`.\n\n" +
		"Often empty early in a term — PowerSchool doesn't create these\n" +
		"rows until teachers post assignments against the standards.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")

		client, err := sisClient(cmd)
		if err != nil {
			return err
		}
		dcid, err := resolveStudentDCID(client)
		if err != nil {
			return err
		}
		yearID, err := resolveYearID(client, dcid, sisStandardGradesYear)
		if err != nil {
			return err
		}
		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return err
		}
		sectionIDs := make([]int64, 0, len(stu.Enrollments))
		sectionName := map[int64]string{}
		for _, e := range stu.Enrollments {
			if sisStandardGradesSection == 0 || e.SectionDCID == sisStandardGradesSection {
				sectionIDs = append(sectionIDs, e.SectionDCID)
				sectionName[e.SectionDCID] = e.Section.CourseName
			}
		}
		if len(sectionIDs) == 0 {
			if sisStandardGradesSection != 0 {
				return fmt.Errorf("section %d is not among this student's enrollments for year %d", sisStandardGradesSection, yearID)
			}
			return fmt.Errorf("no enrollments for school year %d; pass --year <id> to select a different year", yearID)
		}
		rows, err := client.GetStandardGrades(dcid, sectionIDs, yearID)
		if err != nil {
			return err
		}
		out := sisStandardGradeRows(rows, sectionName)

		if asJSON {
			j := map[string]any{
				"count":           len(out),
				"year_id":         yearID,
				"section_ids":     sectionIDs,
				"standard_grades": out,
			}
			b, _ := json.MarshalIndent(j, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		if len(out) == 0 {
			fmt.Fprintln(os.Stderr, "No standard-grade rows posted yet (PowerSchool returns [] before teachers post assignments).")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d standard-grade row(s) across %d section(s) (year %d)\n", len(out), len(sectionIDs), yearID)
		tableRows := make([][]string, len(out))
		for i, r := range out {
			tableRows[i] = []string{
				r.Course,
				r.StoreCode,
				fmt.Sprintf("%d", r.StandardID),
				fmt.Sprintf("%d", r.SectionDCID),
				gradeOrDash(r.Grade),
				formatPercent(r.Percent),
				joinFlags(r.Flags),
			}
		}
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Course", "Store", "Standard", "Section", "Grade", "%", "Flags"},
			Widths: table.Widths(
				[]table.ColumnSpec{
					table.Flex(20), table.Fixed(6), table.Fixed(10),
					table.Fixed(10), table.Fixed(6), table.Fixed(7), table.Flex(12),
				},
				table.TerminalWidth()),
			Rows: tableRows,
			Wrap: true,
		}))
		return nil
	},
}

func gradeOrDash(grade string) string {
	if grade == "" {
		return "—"
	}
	return grade
}

func joinFlags(flags []string) string {
	return strings.Join(flags, ",")
}

func init() {
	sisStandardGradesCmd.Flags().IntVar(&sisStandardGradesYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisStandardGradesCmd.Flags().Int64Var(&sisStandardGradesSection, "section", 0, "Limit to a single section DCID (default: every enrolled section)")
	sisCmd.AddCommand(sisStandardGradesCmd)
}
