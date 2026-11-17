package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	stderrors "github.com/IamCoder18/schooltools/internal/errors"
	"github.com/IamCoder18/schooltools/internal/ps"
	"github.com/IamCoder18/schooltools/internal/table"
)

var sisWhoamiYear int

var sisWhoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Verify the saved SIS session and print the student profile",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")

		client, err := sisClient(cmd)
		if err != nil {
			return err
		}
		dcid, err := resolveStudentDCID(client)
		if err != nil {
			return fmt.Errorf("could not resolve student DCID: %w", err)
		}
		yearID, err := resolveYearID(client, dcid, sisWhoamiYear)
		if err != nil {
			return fmt.Errorf("/ws/xte/standard_grade_section/student/years failed: %w", err)
		}
		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return fmt.Errorf("/ws/xte/student/%d/enrollments failed: %w", dcid, err)
		}

		if asJSON {
			out := map[string]any{
				"count":       1,
				"dcid":        stu.DCID,
				"id":          stu.ID,
				"person_id":   stu.PersonID,
				"student_no":  stu.StudentNo,
				"name":        stu.LastFirst,
				"first_name":  stu.FirstName,
				"last_name":   stu.LastName,
				"grade_level": stu.GradeLevel,
				"school_id":   stu.SchoolID,
				"sections":    len(stu.Enrollments),
				"year_id":     yearID,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}

		fmt.Fprintf(os.Stderr, "Verified: PowerSchool accepted the saved session.\n")
		out := table.Render(table.Options{
			Headers: []string{"Field", "Value"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(14), table.Flex(32)}, table.TerminalWidth()),
			Rows: [][]string{
				{"DCID", fmt.Sprintf("%d", stu.DCID)},
				{"Student No", stu.StudentNo},
				{"Name", stu.LastFirst},
				{"Grade", fmt.Sprintf("%d", stu.GradeLevel)},
				{"School ID", fmt.Sprintf("%d", stu.SchoolID)},
				{"Year ID", fmt.Sprintf("%d", yearID)},
				{"Sections", fmt.Sprintf("%d", len(stu.Enrollments))},
			},
		})
		fmt.Print(out)
		return nil
	},
}

func init() {
	sisWhoamiCmd.Flags().IntVar(&sisWhoamiYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisCmd.AddCommand(sisWhoamiCmd)
}

// resolveStudentDCID returns the logged-in student's DCID. The
// PowerSchool year-picker endpoint needs it.
//
// Precedence:
//  1. SIS_DCID env var (skip the scrape).
//  2. Scrape /guardian/home.html — PowerSchool's Angular SPA embeds
//     the student record inside an inline bootstrap script.
//
// The dashboard is part of the live PS HTML surface; it's the same
// authenticated path the SPA uses, so a successful GET implies a
// working session.
func resolveStudentDCID(client *ps.Client) (int64, error) {
	if v := os.Getenv("SIS_DCID"); v != "" {
		var d int64
		if _, err := fmt.Sscanf(v, "%d", &d); err == nil && d > 0 {
			return d, nil
		}
	}
	body, err := client.GetDashboardHTML()
	if err != nil {
		return 0, err
	}
	dcid := ps.ExtractDCIDFromDashboard(body)
	if dcid == 0 {
		if !strings.Contains(body, "credentials.html") {
			return 0, stderrors.New("PS_SESSION_STALE",
				fmt.Sprintf("PowerSchool returned %d bytes without the guardian dashboard; the saved SIS session is likely expired — run 'schooltools sis login'", len(body)))
		}
		return 0, stderrors.New("PS_DCID_PARSE",
			fmt.Sprintf("could not find the student DCID in the guardian dashboard (%d bytes); set SIS_DCID in the env file or run 'schooltools sis login'", len(body)))
	}
	return dcid, nil
}
