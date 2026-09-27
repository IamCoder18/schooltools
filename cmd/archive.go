package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/archive"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/ui"
)

// tsvSanitize replaces tabs and newlines so a single field can never break
// out of its column or its row. Used by every --plain TSV emitter.
func tsvSanitize(s string) string {
	s = strings.ReplaceAll(s, "\t", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// tsvWriteLine writes one sanitised tab-separated row terminated by a newline.
// Returning the write error keeps lint happy without wrapping the entire
// caller in a fmt.Fprintln chain.
func tsvWriteLine(w io.Writer, fields ...string) error {
	for i, f := range fields {
		if i > 0 {
			if _, err := io.WriteString(w, "\t"); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(w, tsvSanitize(f)); err != nil {
			return err
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

var (
	archiveDir       string
	archiveJSON      bool
	archivePlain     bool
	archiveQuiet     bool
	archiveVerbose   bool
	archiveNoSpinner bool
	archiveCourses   []string
	archiveEnvFile   = ".env"

	archiveUpdateDryRun    bool
	archiveUpdateNoBodies  bool
	archiveUpdateNoRefresh bool
	archiveUpdateWait      bool

	archiveFindExt  string
	archiveFindType string
	archiveFindWith bool
	archiveFindMiss bool

	archiveShowKind string

	archiveCatMeta bool

	archivePathKind string

	archiveExportOut    string
	archiveExportFlat   bool
	archiveExportDryRun bool
	archiveExportExt    string

	archivePruneDelete bool
	archivePruneWait   bool

	archiveVerifyDeep bool
)

const staleThreshold = 24 * time.Hour

func init() {
	archiveCmd.PersistentFlags().StringVar(&archiveDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
	archiveCmd.PersistentFlags().BoolVar(&archiveJSON, "json", false, "Emit JSON envelopes (collections: {count, items}; records: object)")
	archiveCmd.PersistentFlags().BoolVar(&archivePlain, "plain", false, "Emit TSV (header row + tab-separated values) for piping into fzf/cut/xargs")
	archiveCmd.PersistentFlags().BoolVarP(&archiveQuiet, "quiet", "q", false, "Suppress non-essential stderr messages")
	archiveCmd.PersistentFlags().BoolVarP(&archiveVerbose, "verbose", "v", false, "Print per-topic progress to stderr")
	archiveCmd.PersistentFlags().BoolVar(&archiveNoSpinner, "no-spinner", false, "Disable the spinner / progress bar")
	archiveCmd.PersistentFlags().StringArrayVar(&archiveCourses, "course", nil, "Restrict to one or more course OrgUnitIds (repeatable)")

	archiveUpdateCmd.Flags().BoolVar(&archiveUpdateDryRun, "dry-run", false, "Plan only; do not write to disk or fetch bodies")
	archiveUpdateCmd.Flags().BoolVar(&archiveUpdateNoBodies, "no-bodies", false, "Skip file-body downloads (metadata only)")
	archiveUpdateCmd.Flags().BoolVar(&archiveUpdateNoRefresh, "no-auto-refresh", false, "Do not re-run login if the saved session has expired")
	archiveUpdateCmd.Flags().BoolVar(&archiveUpdateWait, "wait", false, "Block instead of failing when another archive run holds the lock")

	archiveFindCmd.Flags().StringVar(&archiveFindExt, "ext", "", "Filter by URL extension (e.g. pdf, docx)")
	archiveFindCmd.Flags().StringVar(&archiveFindType, "type", "", "Filter by topic type (File, Link)")
	archiveFindCmd.Flags().BoolVar(&archiveFindWith, "with-bodies", false, "Only show topics that have a file body archived")
	archiveFindCmd.Flags().BoolVar(&archiveFindMiss, "missing-bodies", false, "Only show File topics whose body has never been archived")

	archiveShowCmd.Flags().StringVar(&archiveShowKind, "kind", "", "Force the ref kind: metadata|body (default: auto-detect)")

	archiveCatCmd.Flags().BoolVar(&archiveCatMeta, "meta", false, "Force the metadata blob instead of the file body")

	archivePathCmd.Flags().StringVar(&archivePathKind, "kind", "", "Force the blob kind: metadata|body (auto-detected otherwise)")

	archiveExportCmd.Flags().StringVar(&archiveExportOut, "out", "", "Destination directory (required)")
	archiveExportCmd.Flags().BoolVar(&archiveExportFlat, "flat", false, "Skip the per-course folder layout")
	archiveExportCmd.Flags().BoolVar(&archiveExportDryRun, "dry-run", false, "Print the copy plan without writing files")
	archiveExportCmd.Flags().StringVar(&archiveExportExt, "ext", "", "Filter by URL extension")

	archivePruneCmd.Flags().BoolVar(&archivePruneDelete, "delete", false, "Apply the prune (default: print the plan only)")
	archivePruneCmd.Flags().BoolVar(&archivePruneWait, "wait", false, "Block instead of failing when another run holds the lock")

	archiveVerifyCmd.Flags().BoolVar(&archiveVerifyDeep, "deep", false, "Re-hash every body blob and compare to its filename")

	archiveCmd.AddCommand(
		archiveUpdateCmd,
		archiveListCmd,
		archiveFindCmd,
		archiveShowCmd,
		archiveCatCmd,
		archivePathCmd,
		archiveExportCmd,
		archivePruneCmd,
		archiveVerifyCmd,
	)
	rootCmd.AddCommand(archiveCmd)
}

var archiveCmd = &cobra.Command{
	Use:   "archive [command]",
	Short: "Archive D2L courses into a global on-disk tree, fetch changed documents, search and export",
	Long: "Snapshot your D2L courses into a versioned on-disk store.\n" +
		"Bare `archive` shows store status (counts, missing bodies, freshness);\n" +
		"`archive update` fetches new and changed documents; `archive find`,\n" +
		"`archive show`, `archive cat`, `archive path`, `archive export`,\n" +
		"`archive prune`, and `archive verify` cover lookup and maintenance.\n\n" +
		"Examples:\n" +
		"  schooltools archive                          # status\n" +
		"  schooltools archive update                   # fetch everything\n" +
		"  schooltools archive update 12345 67890       # fetch two courses\n" +
		"  schooltools archive update --dry-run         # preview the plan\n" +
		"  schooltools archive find safety contract     # search titles\n" +
		"  schooltools archive cat 19448654 > doc.pdf   # bytes to stdout\n" +
		"  schooltools archive export --out ~/pdfs --ext pdf",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveStatus()
	},
}

func runArchiveStatus() error {
	st, err := archive.LoadStatus(archiveDir)
	if err != nil {
		return err
	}
	if !st.Exists {
		if archiveJSON {
			out, _ := json.MarshalIndent(st, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		fmt.Printf("No archive yet at %s\n", st.Root)
		fmt.Println("Run `schooltools archive update` to start.")
		return nil
	}

	if archiveJSON {
		out, _ := json.MarshalIndent(st, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	freshness := relTime(st.UpdatedAt)
	stalenessWarn := ""
	if !st.UpdatedAt.IsZero() && time.Since(st.UpdatedAt) > staleThreshold {
		stalenessWarn = "  (stale — run `archive update`)"
	}
	fmt.Printf("Archive at %s\n", st.Root)
	if st.UpdatedAt.IsZero() {
		fmt.Println("Last update: never")
	} else {
		fmt.Printf("Last update: %s%s\n", freshness, stalenessWarn)
	}
	fmt.Printf("Schema: v%d (store v%d)\n", st.SchemaVersion, st.StoreVersion)
	fmt.Printf("Courses: %d    Topics: %d    File topics: %d\n", st.Courses, st.Topics, st.FileTopics)
	fmt.Printf("Bodies: %d present, %d missing\n", st.BodiesPresent, st.MissingBodies)
	fmt.Printf("Blobs:  %d metadata (%s) + %d bodies (%s)\n",
		st.MetaBlobs, humanBytes(st.MetaBytes),
		st.BodyBlobs, humanBytes(st.BodyBytes))
	if st.LockHeld {
		fmt.Println("Lock:   held by another run")
	} else {
		fmt.Println("Lock:   free")
	}
	fmt.Println()
	fmt.Println("Try next:")
	if st.MissingBodies > 0 {
		fmt.Printf("  schooltools archive update --course %s   # fill %d missing body\n",
			pickFirstCourse(st), st.MissingBodies)
	}
	if st.FileTopics > 0 {
		fmt.Printf("  schooltools archive find --type File --with-bodies --plain | fzf\n")
	}
	fmt.Println("  schooltools archive verify                 # integrity check")
	fmt.Println("  schooltools archive prune                  # plan; --delete to apply")
	return nil
}

func pickFirstCourse(st archive.Status) string {
	store, err := archive.OpenStore(st.Root)
	if err != nil || store == nil {
		return "<courseId>"
	}
	for _, cid := range archive.SortedCourseIDs(store.Index) {
		return cid
	}
	return "<courseId>"
}

var archiveUpdateCmd = &cobra.Command{
	Use:   "update [courseId...]",
	Short: "Fetch new and changed documents (and any missing file bodies) into the archive",
	Long: "Run an archive pass over your D2L courses. Default scope is every course\n" +
		"returned by the manageCourses API; pass one or more OrgUnitIds as positional\n" +
		"arguments or via repeated --course to restrict the pass.\n\n" +
		"`archive update` converges: it downloads bodies for any File topic whose\n" +
		"body is missing on disk, even when its metadata is unchanged. Pass\n" +
		"--no-bodies to skip body downloads. Pass --dry-run to preview without\n" +
		"writing.\n\n" +
		"Examples:\n" +
		"  schooltools archive update\n" +
		"  schooltools archive update 12345 67890\n" +
		"  schooltools archive update --course 12345 --dry-run\n" +
		"  schooltools archive update --no-bodies --no-auto-refresh",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveUpdate(args)
	},
}

func runArchiveUpdate(args []string) error {
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return err
	}

	if archiveUpdateDryRun {
		return runArchiveUpdateDryRun(args, abs)
	}

	lock, err := acquireLock(abs, archiveUpdateWait)
	if err != nil {
		if errors.Is(err, archive.ErrAlreadyRunning) {
			return fmt.Errorf("another archive run holds the lock at %s; re-run with --wait to block", archive.LockPath(abs))
		}
		return fmt.Errorf("acquire archive lock: %w", err)
	}
	if lock != nil {
		defer func() { _ = lock.Release() }()
	}

	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     archiveEnvFile,
		AutoRefresh: !archiveUpdateNoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}

	var hud *ui.Progress
	if !archiveNoSpinner && !archiveJSON && !archiveQuiet {
		hud = ui.NewProgress(ui.ProgressOptions{Title: archiveTitle(append([]string{}, args...), archiveCourses)})
		hud.Start()
		defer hud.Stop()
	}

	scope := mergeScope(args, archiveCourses)
	start := time.Now()
	result, err := archive.Run(archive.Options{
		Root:          abs,
		Jar:           ens.Jar,
		Verbose:       archiveVerbose,
		Quiet:         archiveJSON || archiveQuiet,
		CourseIDs:     scope,
		Progress:      progressAdapter(hud),
		DownloadFiles: !archiveUpdateNoBodies,
	})
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"root":           result.Root,
			"courseIds":      scope,
			"coursesScanned": result.CoursesScanned,
			"topicsChecked":  result.TopicsChecked,
			"topicsFetched":  result.TopicsFetched,
			"topicsStale":    result.TopicsStale,
			"bodiesNew":      result.BodiesFetched,
			"bodiesReused":   result.BodiesReused,
			"bodiesFailed":   result.BodiesFailed,
			"elapsedSeconds": elapsed.Seconds(),
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("Archive root: %s\n", result.Root)
	fmt.Printf("Scanned %d course%s in %s\n",
		result.CoursesScanned, plural(result.CoursesScanned), elapsed.Round(time.Millisecond))
	fmt.Printf("Topics: %d checked, %d fetched, %d unchanged\n",
		result.TopicsChecked, result.TopicsFetched, result.TopicsStale)
	if !archiveUpdateNoBodies && result.BodiesFetched+result.BodiesReused+result.BodiesFailed > 0 {
		fmt.Printf("File bodies: %d new, %d unchanged (same SHA), %d errors\n",
			result.BodiesFetched, result.BodiesReused, result.BodiesFailed)
	} else if archiveUpdateNoBodies {
		fmt.Println("File bodies: skipped (--no-bodies)")
	} else {
		fmt.Println("File bodies: 0 fetched (no eligible File topics this pass).")
	}
	return nil
}

func runArchiveUpdateDryRun(args []string, abs string) error {
	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     archiveEnvFile,
		AutoRefresh: !archiveUpdateNoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}
	scope := mergeScope(args, archiveCourses)
	start := time.Now()
	result, err := archive.Diff(archive.Options{
		Root:          abs,
		Jar:           ens.Jar,
		Verbose:       archiveVerbose,
		Quiet:         archiveJSON || archiveQuiet,
		CourseIDs:     scope,
		DownloadFiles: !archiveUpdateNoBodies,
	})
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"root":             result.Root,
			"courseIds":        scope,
			"coursesScanned":   result.CoursesScanned,
			"topicsChecked":    result.TopicsChecked,
			"topicsWouldFetch": result.TopicsWouldFetch,
			"topicsUnchanged":  result.TopicsStale,
			"bodiesWouldFetch": result.BodiesWouldFetch,
			"elapsedSeconds":   elapsed.Seconds(),
			"perCourse":        result.PerCourse,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	printUpdateDryRunTable(result)
	fmt.Printf("\nWould scan %d course%s in %s\n",
		result.CoursesScanned, plural(result.CoursesScanned), elapsed.Round(time.Millisecond))
	fmt.Printf("Topics: %d checked, %d would fetch, %d unchanged\n",
		result.TopicsChecked, result.TopicsWouldFetch, result.TopicsStale)
	if result.BodiesWouldFetch > 0 {
		fmt.Printf("Bodies: %d would fill (convergence)\n", result.BodiesWouldFetch)
	}
	if result.TopicsWouldFetch == 0 && result.BodiesWouldFetch == 0 {
		fmt.Println("Nothing to do — your archive is up to date.")
	} else {
		fmt.Println("Re-run without --dry-run to apply.")
	}
	return nil
}

func printUpdateDryRunTable(result archive.DiffResult) {
	rows := make([][]string, 0, len(result.PerCourse))
	for _, c := range result.PerCourse {
		fetch := "—"
		if len(c.WouldFetchIDs) > 0 {
			fetch = fmt.Sprintf("%d topics", len(c.WouldFetchIDs))
		}
		bodies := "—"
		if c.BodiesWouldFetch > 0 {
			bodies = fmt.Sprintf("%d bodies", c.BodiesWouldFetch)
		}
		rows = append(rows, []string{
			c.CourseID, c.CourseCode, c.CourseName,
			fmt.Sprintf("%d", c.TopicsTotal),
			fmt.Sprintf("%d", c.TopicsNew),
			fmt.Sprintf("%d", c.TopicsModified),
			fmt.Sprintf("%d", c.TopicsUnchanged),
			fetch, bodies,
		})
	}
	if len(rows) == 0 {
		fmt.Println("(no courses matched)")
		return
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Code", "Name", "Total", "New", "Modified", "Unchanged", "WouldFetch", "Bodies"},
		Widths: table.Widths([]table.ColumnSpec{
			table.Fixed(8), table.Fixed(8), table.Flex(20),
			table.Fixed(6), table.Fixed(5), table.Fixed(9),
			table.Fixed(10), table.Fixed(10), table.Flex(8),
		}, table.TerminalWidth()),
		Rows: rows,
	})
	fmt.Print(out)
}

var archiveListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the saved archive index without making any API requests",
	Long: "Print every course known to the archive index. Without --json or --plain,\n" +
		"output is a fitted table; run bare `schooltools archive` for freshness and\n" +
		"missing-body status.\n\n" +
		"Examples:\n" +
		"  schooltools archive list\n" +
		"  schooltools archive list --json | jq '.items[].code'",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveList()
	},
}

func runArchiveList() error {
	store, err := archive.OpenStore(archiveDir)
	if err != nil {
		return err
	}
	cids := storeCourses(store)
	if len(cids) == 0 {
		if archiveJSON {
			fmt.Println(`{"count":0,"items":[]}`)
			return nil
		}
		if archivePlain {
			fmt.Println("id\tcode\tname\tactive\ttopics\ttoc")
			return nil
		}
		fmt.Printf("No archive at %s yet — run `schooltools archive update`.\n", store.Root)
		return nil
	}
	if archiveJSON {
		items := make([]archive.IndexEntry, 0, len(cids))
		for _, cid := range cids {
			ci := store.Courses[cid]
			e := store.Index.Courses[cid]
			if e == nil {
				e = &archive.IndexEntry{OrgUnitId: cid, Name: ci.Name, Code: ci.Code, IsActive: ci.IsActive}
			}
			items = append(items, *e)
		}
		out, _ := json.MarshalIndent(map[string]any{
			"count":     len(items),
			"items":     items,
			"updatedAt": store.Index.UpdatedAt,
			"root":      store.Root,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	if archivePlain {
		if err := tsvWriteLine(os.Stdout, "id", "code", "name", "active", "topics", "toc"); err != nil {
			return err
		}
		for _, cid := range cids {
			e := store.Index.Courses[cid]
			ci := store.Courses[cid]
			toc := ""
			if e != nil && !e.TocFetchedAt.IsZero() {
				toc = relTime(e.TocFetchedAt)
			}
			active := "yes"
			if ci != nil && !ci.IsActive {
				active = "no"
			}
			row := e
			if row == nil {
				row = &archive.IndexEntry{Name: ci.Name, Code: ci.Code}
			}
			if err := tsvWriteLine(os.Stdout,
				cid, row.Code, row.Name, active,
				strconv.Itoa(len(ci.Topics)), toc,
			); err != nil {
				return err
			}
		}
		return nil
	}
	rows := make([][]string, 0, len(cids))
	for _, cid := range cids {
		e := store.Index.Courses[cid]
		ci := store.Courses[cid]
		active := "—"
		if ci != nil && ci.IsActive {
			active = "yes"
		}
		toc := "—"
		if e != nil && !e.TocFetchedAt.IsZero() {
			toc = relTime(e.TocFetchedAt)
		}
		rows = append(rows, []string{
			cid, active, codeOf(e, ci), nameOf(e, ci),
			fmt.Sprintf("%d", len(ci.Topics)),
			toc,
		})
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Active", "Code", "Name", "Topics", "TOC fetched"},
		Widths: table.Widths([]table.ColumnSpec{
			table.Fixed(8), table.Fixed(6), table.Fixed(8), table.Flex(20), table.Fixed(6), table.Flex(10),
		}, table.TerminalWidth()),
		Rows: rows,
	})
	fmt.Print(out)
	return nil
}

func codeOf(e *archive.IndexEntry, ci *archive.CourseIndex) string {
	if e != nil && e.Code != "" {
		return e.Code
	}
	if ci != nil {
		return ci.Code
	}
	return "—"
}

func nameOf(e *archive.IndexEntry, ci *archive.CourseIndex) string {
	if e != nil && e.Name != "" {
		return e.Name
	}
	if ci != nil {
		return ci.Name
	}
	return "—"
}

func storeCourses(store *archive.Store) []string {
	if len(archiveCourses) > 0 {
		want := map[string]struct{}{}
		for _, c := range archiveCourses {
			want[strings.TrimSpace(c)] = struct{}{}
		}
		out := []string{}
		for _, cid := range storeCoursesAll(store) {
			if _, ok := want[cid]; ok {
				out = append(out, cid)
			}
		}
		return out
	}
	return storeCoursesAll(store)
}

func storeCoursesAll(store *archive.Store) []string {
	out := make([]string, 0, len(store.Courses))
	for cid := range store.Courses {
		out = append(out, cid)
	}
	sort.Slice(out, func(i, j int) bool {
		ai, _ := strconv.Atoi(out[i])
		aj, _ := strconv.Atoi(out[j])
		if ai != 0 && aj != 0 {
			return ai < aj
		}
		return out[i] < out[j]
	})
	return out
}

var archiveFindCmd = &cobra.Command{
	Use:   "find [query...]",
	Short: "Search archived topics by title / URL / type",
	Long: "Walk every course's index once and match topics whose title, URL, or\n" +
		"type contains all whitespace-separated query tokens (case-insensitive).\n" +
		"All tokens must match (AND). Use --ext to filter by file extension,\n" +
		"--type File|Link to filter by topic type, --with-bodies for topics\n" +
		"with an archived file body, --missing-bodies for File topics whose\n" +
		"body has never been archived.\n\n" +
		"Examples:\n" +
		"  schooltools archive find safety contract\n" +
		"  schooltools archive find --type File --ext pdf\n" +
		"  schooltools archive find --with-bodies --plain | fzf",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveFind(args)
	},
}

func runArchiveFind(args []string) error {
	store, err := archive.OpenStore(archiveDir)
	if err != nil {
		return err
	}
	q := archive.SearchQuery{
		Terms:         strings.Join(args, " "),
		Ext:           archiveFindExt,
		CourseIDs:     archiveCourses,
		Type:          archiveFindType,
		WithBodies:    archiveFindWith,
		MissingBodies: archiveFindMiss,
	}
	hits := store.Search(q)

	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"count": len(hits),
			"items": hits,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	if archivePlain {
		if err := tsvWriteLine(os.Stdout, "topicId", "course", "code", "ext", "hasBody", "title", "url"); err != nil {
			return err
		}
		for _, h := range hits {
			ext := strings.TrimPrefix(filepath.Ext(h.URL), ".")
			if ext == "" {
				ext = "—"
			}
			hb := "no"
			if h.HasBody {
				hb = "yes"
			}
			if err := tsvWriteLine(os.Stdout,
				strconv.Itoa(h.TopicID), h.CourseID, h.CourseCode, ext, hb, h.Title, h.URL,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if len(hits) == 0 {
		fmt.Println("No topics matched.")
		return nil
	}
	rows := make([][]string, len(hits))
	for i, h := range hits {
		ext := strings.TrimPrefix(filepath.Ext(h.URL), ".")
		if ext == "" {
			ext = "—"
		}
		hb := "—"
		if h.HasBody {
			hb = "yes"
		}
		rows[i] = []string{
			fmt.Sprintf("%d", h.TopicID),
			h.CourseCode,
			ext,
			h.Type,
			hb,
			relTime(h.LastModified),
			truncate(h.Title, 50),
		}
	}
	out := table.Render(table.Options{
		Headers: []string{"TopicId", "Code", "Ext", "Type", "Body", "Modified", "Title"},
		Widths: table.Widths([]table.ColumnSpec{
			table.Fixed(8), table.Fixed(8), table.Fixed(6),
			table.Fixed(6), table.Fixed(6), table.Flex(10), table.Flex(24),
		}, table.TerminalWidth()),
		Rows: rows,
	})
	fmt.Print(out)
	fmt.Printf("\n%d match%s\n", len(hits), plural(len(hits)))
	return nil
}

var archiveShowCmd = &cobra.Command{
	Use:   "show <ref>",
	Short: "Show one archived topic's record, current blob pointers, and version history",
	Long: "<ref> is auto-detected: a numeric topicId, a 32-hex metadata uuid, or a\n" +
		"64-hex body sha256. --kind forces metadata|body when ambiguous.\n\n" +
		"Examples:\n" +
		"  schooltools archive show 19448654\n" +
		"  schooltools archive show 13d4b7a480969f146225f65e811a3621\n" +
		"  schooltools archive show c1a8e46b…472b --kind body",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveShow(args[0])
	},
}

func runArchiveShow(ref string) error {
	store, err := archive.OpenStore(archiveDir)
	if err != nil {
		return err
	}
	res, err := store.Resolve(ref, archiveShowKind)
	if err != nil {
		return err
	}
	if res.Ref.Topic == nil {
		return fmt.Errorf("%s blob %s exists at %s but no archived topic references it (try: schooltools archive find <title words>)",
			res.Kind, res.BlobID, res.BlobPath)
	}
	t := res.Ref.Topic
	if archiveJSON {
		out := map[string]any{
			"topicId":      t.TopicID,
			"title":        t.Title,
			"type":         t.Type,
			"url":          t.URL,
			"lastModified": t.LastModified,
			"current":      t.Current,
			"versions":     t.Versions,
			"currentBody":  t.CurrentBody,
			"bodyVersions": t.BodyVersions,
			"courseId":     res.Ref.CourseID,
			"courseCode":   res.Ref.CourseCode,
			"courseName":   res.Ref.CourseName,
		}
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if archivePlain {
		if err := tsvWriteLine(os.Stdout,
			"topicId", "course", "code", "title", "type", "current", "currentBody", "versions", "bodyVersions",
		); err != nil {
			return err
		}
		return tsvWriteLine(os.Stdout,
			strconv.Itoa(t.TopicID), res.Ref.CourseID, res.Ref.CourseCode, t.Title, t.Type,
			t.Current, t.CurrentBody,
			strconv.Itoa(len(t.Versions)), strconv.Itoa(len(t.BodyVersions)),
		)
	}
	fmt.Printf("Archived topic %d in course %s (%s, %q)\n",
		t.TopicID, res.Ref.CourseID, res.Ref.CourseCode, res.Ref.CourseName)
	if t.Title != "" {
		fmt.Printf("  title:        %s\n", t.Title)
	}
	fmt.Printf("  type:         %s\n", t.Type)
	if t.URL != "" {
		fmt.Printf("  url:          %s\n", t.URL)
	}
	if !t.LastModified.IsZero() {
		fmt.Printf("  lastModified: %s (%s)\n",
			t.LastModified.UTC().Format("2006-01-02T15:04:05Z"), relTime(t.LastModified))
	}
	fmt.Printf("\n  metadata current: %s (%d version%s on record)\n",
		t.Current, len(t.Versions), plural(len(t.Versions)))
	for i, v := range t.Versions {
		fmt.Printf("    meta v%-3d uuid=%s  size=%d  saved=%s",
			i+1, v.UUID, v.Size, v.SavedAt.UTC().Format("2006-01-02T15:04:05Z"))
		if !v.LastModified.IsZero() {
			fmt.Printf("  lastMod=%s", v.LastModified.UTC().Format("2006-01-02T15:04:05Z"))
		}
		fmt.Println()
	}
	if t.CurrentBody != "" {
		fmt.Printf("\n  body current:    %s (%d version%s on record)\n",
			t.CurrentBody, len(t.BodyVersions), plural(len(t.BodyVersions)))
		fmt.Printf("  body path:       %s\n", archive.BodyPath(archiveDir, t.CurrentBody))
		for i, b := range t.BodyVersions {
			ct := ""
			if b.ContentType != "" {
				ct = "  type=" + b.ContentType
			}
			fmt.Printf("    body v%-3d sha=%s  size=%d  downloaded=%s%s\n",
				i+1, b.SHA256, b.Size, b.DownloadedAt.UTC().Format("2006-01-02T15:04:05Z"), ct)
		}
	} else if t.Type == "File" && t.URL != "" {
		fmt.Println("\n  (no file body archived yet — run `archive update` to fill it)")
	}
	return nil
}

var archiveCatCmd = &cobra.Command{
	Use:   "cat <ref>",
	Short: "Write a topic's bytes (File) or pretty-printed JSON (any other type) to stdout",
	Long: "<ref> accepts the same grammar as `archive show`. For File topics with an\n" +
		"archived body, the raw bytes are written to stdout (redirect to a .pdf,\n" +
		".docx, etc.). For everything else the metadata blob is pretty-printed.\n" +
		"--meta forces the metadata blob even for File topics.\n\n" +
		"Examples:\n" +
		"  schooltools archive cat 19448654 > contract.pdf\n" +
		"  schooltools archive cat 13d4b7a480969f146225f65e811a3621 | jq",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveCat(args[0])
	},
}

func runArchiveCat(ref string) error {
	store, err := archive.OpenStore(archiveDir)
	if err != nil {
		return err
	}
	res, err := store.Resolve(ref, "")
	if err != nil {
		return err
	}
	if archiveCatMeta {
		if res.Ref.Topic == nil {
			return fmt.Errorf("--meta requires a topic ref (got a %s blob %s at %s); try: schooltools archive show %s",
				res.Kind, res.BlobID, res.BlobPath, ref)
		}
		res.Kind = "metadata"
		res.BlobID = res.Ref.Topic.Current
		res.BlobPath = archive.MetadataBlobPath(archiveDir, res.BlobID)
	}
	if res.Kind == "body" {
		body, err := archive.LoadFileBody(archiveDir, res.BlobID)
		if err != nil {
			return err
		}
		if body == nil {
			return fmt.Errorf("body blob %s not found on disk", res.BlobID)
		}
		_, err = os.Stdout.Write(body)
		return err
	}
	if res.BlobID == "" {
		return fmt.Errorf("topic has no metadata blob archived")
	}
	blob, err := archive.LoadBlob(archiveDir, res.BlobID)
	if err != nil {
		return err
	}
	if blob == nil {
		return fmt.Errorf("metadata blob %s not found on disk", res.BlobID)
	}
	var parsed any
	if json.Unmarshal(blob, &parsed) == nil {
		out, _ := json.MarshalIndent(parsed, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	fmt.Println(string(blob))
	return nil
}

var archivePathCmd = &cobra.Command{
	Use:   "path <ref>",
	Short: "Print the absolute on-disk path to an archive blob (topicId, metadata uuid, or body sha)",
	Long: "Auto-detected: numeric topicId, 32-hex metadata uuid, or 64-hex body sha.\n" +
		"--kind metadata|body forces one kind when the ref is ambiguous.\n\n" +
		"Examples:\n" +
		"  cat \"$(schooltools archive path 19448654)\"\n" +
		"  cp \"$(schooltools archive path c1a8e46b…472b)\" ~/doc.pdf",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchivePath(args[0])
	},
}

func runArchivePath(ref string) error {
	store, err := archive.OpenStore(archiveDir)
	if err != nil {
		return err
	}
	kind := archivePathKind
	if kind != "" && (kind != "metadata" && kind != "body") {
		return fmt.Errorf("unknown --kind %q (want metadata or body)", kind)
	}
	res, err := store.Resolve(ref, kind)
	if err != nil {
		return err
	}
	fmt.Println(res.BlobPath)
	return nil
}

var archiveExportCmd = &cobra.Command{
	Use:   "export [ref...]",
	Short: "Copy archived file bodies out as CourseCode/Friendly Title.<ext>",
	Long: "Copy every File topic with an archived body to <out>/<CourseCode>/<title>.<ext>.\n" +
		"Collisions get a ` (2)`, ` (3)`, … suffix. Pass refs to copy just one\n" +
		"(`archive export 19448654`). Use --flat to skip the per-course folder.\n" +
		"Use --dry-run to print the plan without writing files.\n\n" +
		"Examples:\n" +
		"  schooltools archive export --out ~/pdfs --course 1522695 --ext pdf\n" +
		"  schooltools archive export --out ~/pdfs --flat\n" +
		"  schooltools archive export --out ./all --dry-run\n" +
		"  schooltools archive export 19448654 --out ./single",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveExport(args)
	},
}

func runArchiveExport(refs []string) error {
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return err
	}
	if archiveExportOut == "" {
		return fmt.Errorf("export: --out is required")
	}
	res, err := archive.Export(archive.ExportOptions{
		Root:      abs,
		Out:       archiveExportOut,
		CourseIDs: archiveCourses,
		Ext:       archiveExportExt,
		Flat:      archiveExportFlat,
		DryRun:    archiveExportDryRun,
		Refs:      refs,
	})
	if err != nil {
		return err
	}
	if archiveJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	if archivePlain {
		if err := tsvWriteLine(os.Stdout, "topicId", "course", "status", "size", "dest"); err != nil {
			return err
		}
		for _, f := range res.Files {
			if err := tsvWriteLine(os.Stdout,
				strconv.Itoa(f.TopicID), f.CourseID, f.Status,
				strconv.FormatInt(f.Size, 10), f.Dest,
			); err != nil {
				return err
			}
		}
		return nil
	}
	if len(res.Files) == 0 {
		fmt.Println("Nothing matched.")
		return nil
	}
	for _, f := range res.Files {
		status := f.Status
		switch f.Status {
		case "copied", "would-copy":
			fmt.Printf("  %s  %s  %s  %s\n", f.CourseID, status, humanBytes(f.Size), f.Dest)
		case "skipped-missing-body":
			fmt.Printf("  %s  SKIP (no body)  topic %d  %s\n", f.CourseID, f.TopicID, f.Title)
		}
	}
	fmt.Printf("\n%s: %d file%s%s, %s total\n",
		statusLabel(res.DryRun), res.Copied, plural(res.Copied),
		fmt.Sprintf(", %d skipped", res.Skipped),
		humanBytes(res.BytesTotal))
	return nil
}

func statusLabel(dry bool) string {
	if dry {
		return "would copy"
	}
	return "copied"
}

var archivePruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Drop old metadata versions and unreferenced blobs; bodies for unreferenced topics",
	Long: "Plan-only by default: prints the count of blobs that would be deleted and\n" +
		"the number of index records that would be trimmed. Pass --delete to apply.\n" +
		"After pruning, per-course indexes only reference blobs that still exist,\n" +
		"so `archive show` can never print a dead uuid.\n\n" +
		"Examples:\n" +
		"  schooltools archive prune              # plan\n" +
		"  schooltools archive prune --delete     # apply\n" +
		"  schooltools archive prune --json       # machine-readable plan",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchivePrune()
	},
}

func runArchivePrune() error {
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return err
	}
	if archivePruneDelete {
		lock, lerr := acquireLock(abs, archivePruneWait)
		if lerr != nil {
			if errors.Is(lerr, archive.ErrAlreadyRunning) {
				return fmt.Errorf("another run holds the lock at %s; re-run with --wait to block", archive.LockPath(abs))
			}
			return fmt.Errorf("acquire archive lock: %w", lerr)
		}
		if lock != nil {
			defer func() { _ = lock.Release() }()
		}
		res, err := archive.Prune(abs)
		if err != nil {
			return err
		}
		return renderPruneResult(abs, res, false)
	}
	res, err := archive.PrunePlan(abs)
	if err != nil {
		return err
	}
	return renderPruneResult(abs, res, true)
}

func renderPruneResult(root string, res archive.PruneResult, plan bool) error {
	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"root":           root,
			"plan":           plan,
			"blobsBefore":    res.BlobsBefore,
			"blobsAfter":     res.BlobsAfter,
			"wouldDelete":    res.Deleted,
			"bytesReclaim":   res.BytesReclaim,
			"orphans":        res.Orphans,
			"trimmedRecords": res.TrimmedRecords,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	verb := "Would delete"
	if !plan {
		verb = "Deleted"
	}
	fmt.Printf("Prune at %s\n", root)
	fmt.Printf("  %s %d blob%s (%s).\n",
		verb, res.Deleted, plural(res.Deleted), humanBytes(res.BytesReclaim))
	if res.TrimmedRecords > 0 {
		fmt.Printf("  %s %d index version record%s (history becomes current-only).\n",
			verb, res.TrimmedRecords, plural(res.TrimmedRecords))
	}
	if plan {
		fmt.Println("  Re-run with --delete to apply.")
	}
	if len(res.Orphans) > 0 {
		fmt.Println("  Orphan samples:")
		n := len(res.Orphans)
		if n > 10 {
			n = 10
		}
		for _, o := range res.Orphans[:n] {
			fmt.Printf("    - %s\n", o)
		}
		if len(res.Orphans) > 10 {
			fmt.Printf("    …and %d more\n", len(res.Orphans)-10)
		}
	}
	return nil
}

var archiveVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Check archive integrity (pointer resolution, dangling versions, orphan blobs)",
	Long: "Walks every course's index and confirms that:\n" +
		"  • every TopicIndex.Current points at an existing .json blob\n" +
		"  • every CurrentBody points at an existing .bin blob\n" +
		"  • every Versions / BodyVersions record resolves to a blob on disk\n" +
		"  • per-course index parses\n" +
		"  • every global-index course id has a courses/<id>/ directory\n" +
		"  • orphan blobs are reported (informational; prune clears them)\n\n" +
		"--deep re-hashes every body blob and compares against its filename.\n\n" +
		"Exits 0 on success, 1 when issues are found.\n\n" +
		"Examples:\n" +
		"  schooltools archive verify\n" +
		"  schooltools archive verify --deep --json | jq '.issues'",
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveVerify()
	},
}

func runArchiveVerify() error {
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return err
	}
	res, err := archive.Verify(abs, archiveVerifyDeep)
	if err != nil {
		return err
	}
	if archiveJSON {
		out, _ := json.MarshalIndent(res, "", "  ")
		fmt.Println(string(out))
		if !res.OK {
			return errJSONShown
		}
		return nil
	}
	if res.OK && len(res.Orphans) == 0 {
		fmt.Printf("OK — checked %d topics, %d blobs at %s\n",
			res.CheckedTopics, res.CheckedBlobs, abs)
		return nil
	}
	fmt.Printf("Checked %d topics, %d blobs at %s\n",
		res.CheckedTopics, res.CheckedBlobs, abs)
	if len(res.Issues) > 0 {
		fmt.Printf("Issues (%d):\n", len(res.Issues))
		for _, iss := range res.Issues {
			line := fmt.Sprintf("  [%s]", iss.Code)
			if iss.CourseID != "" {
				line += " course=" + iss.CourseID
			}
			if iss.TopicID != 0 {
				line += fmt.Sprintf(" topic=%d", iss.TopicID)
			}
			if iss.Blob != "" {
				line += " blob=" + iss.Blob
			}
			fmt.Println(line)
			fmt.Printf("      %s\n", iss.Message)
		}
	}
	if len(res.Orphans) > 0 {
		fmt.Printf("Orphan blobs (%d): use `archive prune --delete` to clear.\n", len(res.Orphans))
		n := len(res.Orphans)
		if n > 10 {
			n = 10
		}
		for _, o := range res.Orphans[:n] {
			fmt.Printf("  - %s\n", o)
		}
		if len(res.Orphans) > 10 {
			fmt.Printf("  …and %d more\n", len(res.Orphans)-10)
		}
	}
	if !res.OK {
		return fmt.Errorf("verify found %d issue%s", len(res.Issues), plural(len(res.Issues)))
	}
	return nil
}

func acquireLock(abs string, wait bool) (*archive.Lock, error) {
	if wait {
		return archive.WaitLock(abs)
	}
	return archive.TryLock(abs)
}

func mergeScope(args, flagCourses []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(args)+len(flagCourses))
	for _, s := range args {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range flagCourses {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func archiveTitle(positional []string, flagCourses []string) string {
	all := mergeScope(positional, flagCourses)
	switch len(all) {
	case 0:
		return "Archiving all courses"
	case 1:
		return fmt.Sprintf("Archiving course %s", all[0])
	default:
		return fmt.Sprintf("Archiving %d courses", len(all))
	}
}

func progressAdapter(hud *ui.Progress) archive.ProgressFunc {
	if hud == nil {
		return nil
	}
	return func(ev archive.ProgressEvent) {
		switch ev.Phase {
		case "list":
			if ev.Total > 0 {
				hud.SetTotal(ev.Total)
				hud.SetMessage(fmt.Sprintf("Scanning %d course%s…", ev.Total, plural(ev.Total)))
			} else {
				hud.SetMessage("Listing courses…")
			}
		case "toc":
			name := ev.CourseName
			if name == "" {
				name = "course " + ev.CourseID
			}
			hud.SetMessage(fmt.Sprintf("Fetching TOC for %s", name))
			hud.AddCounters(ui.Counters{Checked: 0})
		case "topic":
			hud.SetMessage(fmt.Sprintf("Fetching topic %d (%s)", ev.TopicID, ev.Reason))
		case "course-done":
			hud.Increment(1)
			hud.SetCounters(ui.Counters{
				Checked: ev.Checked,
				Fetched: ev.Fetched,
				Stale:   ev.Stale,
			})
		}
	}
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	default:
		return t.UTC().Format("2006-01-02")
	}
}

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
