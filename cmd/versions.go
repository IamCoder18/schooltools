package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/d2lver"
	"github.com/aarav/schooltools/internal/render"
	"github.com/aarav/schooltools/internal/ua"
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
			return render.PrintJSON(v)
		}
		var b strings.Builder
		b.WriteString(render.Title("D2L Valence API versions"))
		b.WriteString(render.Section("In use"))
		b.WriteString(render.KV(
			render.Row{Key: "lp", Value: v.LP + "  ·  /d2l/api/lp/<v>/..."},
			render.Row{Key: "le", Value: v.LE + "  ·  /d2l/api/le/<v>/..."},
			render.Row{Key: "bas", Value: v.BAS + "  ·  /d2l/api/bas/<v>/..."},
			render.Row{Key: "ep", Value: v.EP + "  ·  /d2l/api/eP/<v>/..."},
		))
		b.WriteString(render.Section("Cache"))
		discovered := render.Muted("never — using constants; run any authenticated command to discover")
		if !v.DiscoveredAt.IsZero() {
			discovered = whenOf(v.DiscoveredAt)
		}
		b.WriteString(render.KV(
			render.Row{Key: "Source", Value: v.Source},
			render.Row{Key: "Discovered", Value: discovered},
		))
		b.WriteString(render.Section("Defaults"))
		b.WriteString(render.KV(
			render.Row{Key: "lp", Value: ua.LPAPIVersion},
			render.Row{Key: "le", Value: ua.LEAPIVersion},
			render.Row{Key: "bas", Value: ua.BASAPIVersion},
			render.Row{Key: "ep", Value: ua.EPAPIVersion},
		))
		b.WriteString(render.Section("Next"))
		b.WriteString(render.Hint("schooltools whoami   to discover the tenant's live API versions"))
		fmt.Print(b.String())
		return nil
	},
}

var versionsJSON bool

func init() {
	versionsCmd.Flags().BoolVar(&versionsJSON, "json", false, "Emit JSON instead of a human table")
	rootCmd.AddCommand(versionsCmd)
}
