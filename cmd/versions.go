package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/d2lver"
)

var versionsCmd = &cobra.Command{
	Use:   "versions",
	Short: "Show the D2L API product versions the CLI will talk to (debug)",
	Long: "Prints the LE/LP/BAS/EP product versions schooltools is currently\n" +
		"using. By default this is the hardcoded fallback (1.47 for LE/LP,\n" +
		"1.6 for BAS, 2.5 for EP). After the first authenticated command runs,\n" +
		"the values reflect the highest version supported by the CBE tenant,\n" +
		"discovered from /d2l/api/<product>/versions/.\n\n" +
		"This command never triggers discovery on its own; run any\n" +
		"authenticated command first (e.g. `whoami`) to populate the cache.",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Display-only: never trigger discovery.
		v := d2lver.Cached()
		if versionsJSON {
			b, _ := json.MarshalIndent(v, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		_, _ = fmt.Fprintf(os.Stdout, "D2L Valence API versions in use:\n")
		_, _ = fmt.Fprintf(os.Stdout, "  lp  %s  /d2l/api/lp/<v>/...\n", v.LP)
		_, _ = fmt.Fprintf(os.Stdout, "  le  %s  /d2l/api/le/<v>/...\n", v.LE)
		_, _ = fmt.Fprintf(os.Stdout, "  bas %s  /d2l/api/bas/<v>/...\n", v.BAS)
		_, _ = fmt.Fprintf(os.Stdout, "  ep  %s  /d2l/api/eP/<v>/...\n", v.EP)
		_, _ = fmt.Fprintf(os.Stdout, "\nSource: %s\n", v.Source)
		if !v.DiscoveredAt.IsZero() {
			_, _ = fmt.Fprintf(os.Stdout, "Discovered at: %s\n", v.DiscoveredAt.UTC().Format("2006-01-02T15:04:05Z"))
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "Discovered at: (never; using constants — run any authenticated command to discover)\n")
		}
		_, _ = fmt.Fprintf(os.Stdout, "\nDefaults (ua.go): lp=1.47 le=1.47 bas=1.6 ep=2.5\n")
		return nil
	},
}

var versionsJSON bool

func init() {
	versionsCmd.Flags().BoolVar(&versionsJSON, "json", false, "Emit JSON instead of a human table")
	rootCmd.AddCommand(versionsCmd)
}
