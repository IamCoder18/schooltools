package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/ps"
	"github.com/aarav/schooltools/internal/table"
)

var sisSchoolYear int

// sisSchoolLimitRow is the flattened JSON shape for one school's
// comment-size configuration.
type sisSchoolLimitRow struct {
	SchoolID                 int   `json:"school_id"`
	SchoolDCID               int64 `json:"school_dcid"`
	MaxFinalGradeCommentSize int   `json:"max_final_grade_comment_size"`
}

func sisSchoolLimitRows(limits []ps.SchoolCommentLimit) []sisSchoolLimitRow {
	out := make([]sisSchoolLimitRow, 0, len(limits))
	for _, l := range limits {
		out = append(out, sisSchoolLimitRow{
			SchoolID:                 l.SchoolID,
			SchoolDCID:               l.SchoolDCID,
			MaxFinalGradeCommentSize: l.MaxFinalGradeCommentSize,
		})
	}
	return out
}

var sisSchoolCmd = &cobra.Command{
	Use:   "school",
	Short: "Print PowerSchool school-level config (e.g. max comment size for the student's school)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")

		client, err := sisClient(cmd)
		if err != nil {
			return err
		}
		limits, err := client.GetSchoolCommentLimits()
		if err != nil {
			return err
		}
		schoolRows := sisSchoolLimitRows(limits)
		// Find the row that matches the logged-in student's school.
		dcid, err := resolveStudentDCID(client)
		if err != nil {
			return err
		}
		yearID, err := resolveYearID(client, dcid, sisSchoolYear)
		if err != nil {
			return err
		}
		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return err
		}
		studentSchool := stu.SchoolID

		if asJSON {
			out := map[string]any{
				"count":             len(limits),
				"student_school_id": studentSchool,
				"year_id":           yearID,
				"limits":            schoolRows,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d school config(s); student is in school ID %d\n", len(limits), studentSchool)
		rows := make([][]string, 0, len(limits))
		highlight := func(s int) string {
			if s == studentSchool {
				return "★ " + fmt.Sprintf("%d", s)
			}
			return fmt.Sprintf("%d", s)
		}
		for _, l := range limits {
			rows = append(rows, []string{
				highlight(l.SchoolID),
				fmt.Sprintf("%d", l.SchoolDCID),
				fmt.Sprintf("%d", l.MaxFinalGradeCommentSize),
			})
		}
		fmt.Print(table.Render(table.Options{
			Headers: []string{"School ID", "School DCID", "Max comment chars"},
			Widths: table.Widths(
				[]table.ColumnSpec{table.Fixed(14), table.Fixed(14), table.Fixed(20)},
				table.TerminalWidth()),
			Rows: rows,
		}))
		return nil
	},
}

func init() {
	sisSchoolCmd.Flags().IntVar(&sisSchoolYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisCmd.AddCommand(sisSchoolCmd)
}
