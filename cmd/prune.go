package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/archive"
)

var (
	pruneDir  string
	pruneDry  bool
	pruneJSON bool
)

var pruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Discard old topic-document versions from the archive",
	Long: "Walk every per-course index under the archive root and delete any blob\n" +
		"in blobs/ that is no longer the 'current' version of its topic. Run this\n" +
		"after one or more `archive` passes to reclaim disk space; topics that\n" +
		"have not changed keep their current blob, and the per-course index,\n" +
		"which records the version history, is left untouched.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if pruneDir == "" {
			pruneDir = archive.DefaultRoot()
		}
		abs, err := filepath.Abs(pruneDir)
		if err != nil {
			return fmt.Errorf("resolve prune dir: %w", err)
		}
		if _, err := os.Stat(abs); err != nil {
			if os.IsNotExist(err) {
				fmt.Printf("Nothing to prune: %s does not exist.\n", abs)
				return nil
			}
			return err
		}

		if pruneDry {
			return runPruneDry(abs)
		}

		res, err := archive.Prune(abs)
		if err != nil {
			return err
		}

		if pruneJSON {
			out, _ := json.MarshalIndent(map[string]any{
				"root":         res.Root,
				"blobsBefore":  res.BlobsBefore,
				"blobsAfter":   res.BlobsAfter,
				"deleted":      res.Deleted,
				"bytesReclaim": res.BytesReclaim,
				"orphanUuids":  res.OrphanUUIDs,
			}, "", "  ")
			fmt.Println(string(out))
			return nil
		}

		fmt.Printf("Pruned %s\n", abs)
		fmt.Printf("  blobs before: %d\n", res.BlobsBefore)
		fmt.Printf("  blobs after:  %d\n", res.BlobsAfter)
		fmt.Printf("  deleted:      %d (%s reclaimed)\n",
			res.Deleted, humanBytes(res.BytesReclaim))
		return nil
	},
}

func init() {
	pruneCmd.Flags().StringVar(&pruneDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
	pruneCmd.Flags().BoolVar(&pruneDry, "dry-run", false, "Show what would be deleted without touching disk")
	pruneCmd.Flags().BoolVar(&pruneJSON, "json", false, "Emit a JSON summary instead of a human-readable report")
	rootCmd.AddCommand(pruneCmd)
}

// runPruneDry enumerates blobs and reports which would be removed, without
// touching disk. The implementation just runs Prune (which already only
// reads) and prints what would have been deleted; the underlying function
// is atomic per-file so a crash mid-run is harmless.
func runPruneDry(abs string) error {
	res, err := archive.Prune(abs)
	if err != nil {
		return err
	}
	fmt.Printf("Prune --dry-run at %s\n", abs)
	fmt.Printf("  blobs before: %d\n", res.BlobsBefore)
	fmt.Printf("  blobs after:  %d\n", res.BlobsAfter)
	fmt.Printf("  would delete: %d (%s)\n", res.Deleted, humanBytes(res.BytesReclaim))
	if len(res.OrphanUUIDs) > 0 {
		fmt.Println("  orphan UUIDs (sample):")
		n := len(res.OrphanUUIDs)
		if n > 10 {
			n = 10
		}
		for _, u := range res.OrphanUUIDs[:n] {
			fmt.Printf("    - %s\n", u)
		}
		if len(res.OrphanUUIDs) > 10 {
			fmt.Printf("    …and %d more\n", len(res.OrphanUUIDs)-10)
		}
	}
	return nil
}

// humanBytes formats a byte count as a short human-readable string.
func humanBytes(n int64) string {
	const (
		KiB = 1 << 10
		MiB = 1 << 20
		GiB = 1 << 30
	)
	switch {
	case n >= GiB:
		return fmt.Sprintf("%.2f GiB", float64(n)/GiB)
	case n >= MiB:
		return fmt.Sprintf("%.2f MiB", float64(n)/MiB)
	case n >= KiB:
		return fmt.Sprintf("%.2f KiB", float64(n)/KiB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
