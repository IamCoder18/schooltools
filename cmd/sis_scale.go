package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/ps"
	"github.com/aarav/schooltools/internal/table"
)

var sisScaleCmd = &cobra.Command{
	Use:   "scale",
	Short: "Show the CBE High School Proficiency Scale and nominal percentages",
	Long: "CBE reports high-school achievement on an eight-level proficiency\n" +
		"scale (EX2 → BG1). This prints every level with the nominal\n" +
		"percentage CBE publishes for it. Levels whose percentage CBE does\n" +
		"not publish are shown with a dash; the calculated percentage for\n" +
		"a real outcome is on the `sis standard-grades` row.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		asJSON, _ := cmd.Flags().GetBool("json")
		levels := ps.Scale()

		if asJSON {
			out := map[string]any{"count": len(levels), "levels": levels}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		rows := make([][]string, len(levels))
		for i, l := range levels {
			percent := "—"
			if l.Percent != nil {
				percent = strconv.Itoa(*l.Percent) + "%"
			}
			rows[i] = []string{l.Code, l.Label, percent}
		}
		fmt.Printf("CBE High School Proficiency Scale (%d levels)\n", len(levels))
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Code", "Level", "Nominal %"},
			Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Flex(24), table.Fixed(12)}, table.TerminalWidth()),
			Rows:    rows,
		}))
		return nil
	},
}

func init() { sisCmd.AddCommand(sisScaleCmd) }
