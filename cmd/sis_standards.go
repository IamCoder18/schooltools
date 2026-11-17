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
	sisStandardsYear int
)

// sisStandardRow is the flattened JSON shape for one rubric outcome.
type sisStandardRow struct {
	StandardID          int64  `json:"standard_id"`
	SectionDCID         int64  `json:"section_dcid"`
	Name                string `json:"name"`
	Identifier          string `json:"identifier,omitempty"`
	Description         string `json:"description,omitempty"`
	SubjectArea         string `json:"subject_area,omitempty"`
	YearID              int    `json:"year_id"`
	DisplayPosition     int    `json:"display_position"`
	ExcludedFromReports bool   `json:"excluded_from_reports"`
	AssignmentAllowed   bool   `json:"assignment_allowed"`
	MaxCommentLength    int    `json:"max_comment_length"`
}

func sisStandardRows(stds []ps.Standard, sectionDCID int64) []sisStandardRow {
	out := make([]sisStandardRow, 0, len(stds))
	for _, s := range stds {
		out = append(out, sisStandardRow{
			StandardID:          s.ID,
			SectionDCID:         sectionDCID,
			Name:                s.Name,
			Identifier:          s.Identifier,
			Description:         s.Description,
			SubjectArea:         s.SubjectArea,
			YearID:              s.YearID,
			DisplayPosition:     s.DisplayPos,
			ExcludedFromReports: s.IsExcluded,
			AssignmentAllowed:   s.IsAssignmentAllowed,
			MaxCommentLength:    s.MaxCommentLength,
		})
	}
	return out
}

var sisStandardsCmd = &cobra.Command{
	Use:   "standards",
	Short: "List standards (rubric items) for every section the student is enrolled in",
	Long: "CBE rebrands PowerSchool's \"Standards / Outcomes\" as \"Stems /\n" +
		"Outcomes / Classes\". Each section has its own set of rubric\n" +
		"items; teachers grade against them.",
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
		yearID, err := resolveYearID(client, dcid, sisStandardsYear)
		if err != nil {
			return err
		}
		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return err
		}
		if len(stu.Enrollments) == 0 {
			return fmt.Errorf("no enrollments for school year %d; pass --year <id> to select a different year", yearID)
		}

		rows := make([]sisStandardRow, 0)
		seen := map[string]bool{}
		for _, e := range stu.Enrollments {
			stds, err := client.GetStandards([]int64{e.SectionDCID})
			if err != nil {
				return err
			}
			for _, r := range sisStandardRows(stds, e.SectionDCID) {
				key := fmt.Sprintf("%d/%d", r.SectionDCID, r.StandardID)
				if seen[key] {
					continue
				}
				seen[key] = true
				rows = append(rows, r)
			}
		}

		if asJSON {
			out := map[string]any{
				"count":         len(rows),
				"year_id":       yearID,
				"section_count": len(stu.Enrollments),
				"standards":     rows,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d standard(s) across %d section(s) (year %d)\n", len(rows), len(stu.Enrollments), yearID)
		tableRows := make([][]string, len(rows))
		for i, s := range rows {
			tableRows[i] = []string{
				fmt.Sprintf("%d", s.StandardID),
				fmt.Sprintf("%d", s.SectionDCID),
				s.SubjectArea,
				s.Identifier,
				s.Name,
				fmt.Sprintf("%d", s.YearID),
			}
		}
		fmt.Print(table.Render(table.Options{
			Headers: []string{"ID", "Section", "Subject", "Identifier", "Name", "Year"},
			Widths: table.Widths(
				[]table.ColumnSpec{
					table.Fixed(8), table.Fixed(10), table.Fixed(10),
					table.Fixed(16), table.Flex(30), table.Fixed(6),
				},
				table.TerminalWidth()),
			Rows: tableRows,
			Wrap: true,
		}))
		return nil
	},
}

func init() {
	sisStandardsCmd.Flags().IntVar(&sisStandardsYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisCmd.AddCommand(sisStandardsCmd)
}
