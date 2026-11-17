package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/ps"
	"github.com/aarav/schooltools/internal/table"
)

// sisYearRow is the flattened JSON shape for one academic year.
type sisYearRow struct {
	Abbr   string `json:"abbr"`
	YearID int    `json:"year_id"`
}

func sisYearRows(years []ps.Year) []sisYearRow {
	out := make([]sisYearRow, 0, len(years))
	for _, y := range years {
		out = append(out, sisYearRow{Abbr: y.Abbr, YearID: y.YearID})
	}
	return out
}

var sisYearsCmd = &cobra.Command{
	Use:   "years",
	Short: "List academic years available for the logged-in student",
	Args:  cobra.NoArgs,
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
		years, err := client.GetYears(dcid)
		if err != nil {
			return err
		}
		yearRows := sisYearRows(years)

		if asJSON {
			out := map[string]any{"count": len(yearRows), "years": yearRows}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		rows := make([][]string, len(yearRows))
		for i, y := range yearRows {
			rows[i] = []string{y.Abbr, fmt.Sprintf("%d", y.YearID)}
		}
		fmt.Fprintf(os.Stderr, "Found %d year(s)\n", len(years))
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Year", "Year ID"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(10)}, table.TerminalWidth()),
			Rows:    rows,
		}))
		return nil
	},
}

func init() { sisCmd.AddCommand(sisYearsCmd) }
