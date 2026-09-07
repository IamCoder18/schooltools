package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/archive"
	"github.com/aarav/schooltools/internal/authlog"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/table"
	"github.com/aarav/schooltools/internal/ui"
)

var (
	archiveDir         string
	archiveEnvFile     string
	archiveAutoRefresh bool
	archiveJSON        bool
	archiveListJSON    bool
	archiveVerbose     bool
	archiveNoSpinner   bool
	archiveDiff        bool
	archiveListDir     string
	archiveFiles       bool // download File-topic bodies alongside metadata; default true
)

var archiveCmd = &cobra.Command{
	Use:   "archive [courseId...]",
	Short: "Archive D2L courses into a global on-disk tree, fetching only changed documents",
	Long: "Run an archive pass over your D2L courses. With no arguments, every course\n" +
		"returned by the manageCourses API is archived; pass one or more OrgUnitIds as\n" +
		"positional arguments to restrict the pass to specific courses.\n\n" +
		"For each course the table of contents is fetched and compared against the\n" +
		"saved toc.json; only topics whose LastModifiedDate has changed (or that are\n" +
		"new) are downloaded. Output is written under ~/.config/schooltools/archive/,\n" +
		"with a global index.json at the root.\n\n" +
		"Subcommands:\n" +
		"  list   show the saved index (was `archive --list`)\n\n" +
		"Flags:\n" +
		"  --diff  show what the next pass would fetch, without writing anything",
	Args: cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		if archiveDiff {
			return runArchiveDiff(args)
		}
		return runArchive(args)
	},
}

var archiveListCmd = &cobra.Command{
	Use:   "list",
	Short: "Show the saved archive index without making any API requests",
	Long:  "Print the saved archive index from the on-disk store. Useful for cron jobs that want to verify a previous archive run completed.",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runArchiveList()
	},
}

func init() {
	archiveCmd.Flags().StringVar(&archiveDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
	archiveCmd.Flags().StringVarP(&archiveEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
	archiveCmd.Flags().BoolVar(&archiveAutoRefresh, "auto-refresh", false, "Re-run 'login' if the session is expired")
	archiveCmd.Flags().BoolVar(&archiveJSON, "json", false, "Emit a JSON summary instead of a table")
	archiveCmd.Flags().BoolVar(&archiveDiff, "diff", false, "Show what the next archive pass would fetch, without writing anything")
	archiveCmd.Flags().BoolVarP(&archiveVerbose, "verbose", "v", false, "Print per-topic progress to stderr")
	archiveCmd.Flags().BoolVar(&archiveNoSpinner, "no-spinner", false, "Disable the spinner / progress bar")
	archiveCmd.Flags().BoolVar(&archiveFiles, "files", true, "Also download each File topic's underlying bytes (PDF, DOCX, etc.) into blobs/<sha256>.bin")

	archiveListCmd.Flags().StringVar(&archiveListDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
	archiveListCmd.Flags().BoolVar(&archiveListJSON, "json", false, "Emit the raw index JSON instead of a table")

	archiveCmd.AddCommand(archiveListCmd, archiveTopicCmd, archiveReadCmd, archiveBodyCmd, archivePathCmd)
	rootCmd.AddCommand(archiveCmd)
}

// archiveTopicCmd dumps a single archived topic's per-course index entry:
// title, type, URL, last-modified, the current blob UUID, and the full
// version history. Differs from `content get --archive <id>` which reads
// the *blob* (raw D2L metadata JSON). `archive topic` reads the per-topic
// pointer from `courses/<id>/index.json`.
var (
	archiveTopicDir    string
	archiveTopicJSON   bool
	archiveTopicCourse string
	archiveTopicFiles  bool // print body-version history in the human table
)

var archiveTopicCmd = &cobra.Command{
	Use:   "topic <topicId>",
	Short: "Show the archived index entry for one topic (current blob + version history)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if archiveTopicDir == "" {
			archiveTopicDir = archive.DefaultRoot()
		}
		if archiveTopicCourse == "" {
			return fmt.Errorf("--course <orgUnitId> is required to look up archived topic %s", args[0])
		}
		tid, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("invalid topicId %q: %w", args[0], err)
		}
		idx, err := archive.LoadCourseIndex(archiveTopicDir, archiveTopicCourse)
		if err != nil {
			return err
		}
		if idx == nil {
			return fmt.Errorf("no archive at %s yet", archiveTopicDir)
		}
		t, ok := idx.Topics[tid]
		if !ok || t == nil {
			return fmt.Errorf("topic %d not found in archived course %s", tid, archiveTopicCourse)
		}
		if archiveTopicJSON {
			out := map[string]any{
				"topicId":       t.TopicID,
				"title":         t.Title,
				"type":          t.Type,
				"url":           t.URL,
				"current":       t.Current,
				"versions":      t.Versions,
				"currentBody":    t.CurrentBody,
				"bodyVersions":  t.BodyVersions,
				"courseId":      idx.CourseID,
			}
			b, _ := json.MarshalIndent(out, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		fmt.Printf("Archived topic %d in course %s (%q)\n", t.TopicID, idx.CourseID, idx.Name)
		fmt.Printf("  title:        %s\n", t.Title)
		fmt.Printf("  type:         %s\n", t.Type)
		if t.URL != "" {
			fmt.Printf("  url:          %s\n", t.URL)
		}
		if !t.LastModified.IsZero() {
			fmt.Printf("  lastModified: %s\n", t.LastModified.UTC().Format("2006-01-02T15:04:05Z"))
		}
		fmt.Printf("  metadata current: %s (latest of %d)\n\n", t.Current, len(t.Versions))
		for i, v := range t.Versions {
			fmt.Printf("  meta v%d  uuid=%s  size=%d  savedAt=%s\n",
				i+1, v.UUID, v.Size, v.SavedAt.UTC().Format("2006-01-02T15:04:05Z"))
			if !v.LastModified.IsZero() {
				fmt.Printf("       lastModified=%s\n", v.LastModified.UTC().Format("2006-01-02T15:04:05Z"))
			}
		}
		if archiveTopicFiles && t.CurrentBody != "" {
			fmt.Printf("\n  body current:   %s (latest of %d)\n", t.CurrentBody, len(t.BodyVersions))
			fmt.Printf("  body path:      %s\n", archive.BodyPath(archiveTopicDir, t.CurrentBody))
			for i, bv := range t.BodyVersions {
				ct := ""
				if bv.ContentType != "" {
					ct = "  type=" + bv.ContentType
				}
				fmt.Printf("  body v%d  sha=%s  size=%d  downloadedAt=%s%s\n",
					i+1, bv.SHA256, bv.Size, bv.DownloadedAt.UTC().Format("2006-01-02T15:04:05Z"), ct)
			}
		} else if archiveTopicFiles && t.URL != "" && t.Type == "File" {
			fmt.Println("\n  (no file body archived yet — run `schooltools archive` with --files to add one)")
		}
		return nil
	},
}

func init() { //nolint:revive  // supplemental init runs after the main one.
	archiveTopicCmd.Flags().StringVar(&archiveTopicDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
	archiveTopicCmd.Flags().StringVar(&archiveTopicCourse, "course", "", "Course OrgUnitId to look the topic up under (required)")
	archiveTopicCmd.Flags().BoolVar(&archiveTopicJSON, "json", false, "Emit JSON instead of the human table")
archiveTopicCmd.Flags().BoolVar(&archiveTopicFiles, "files", true, "Print body-version history in the human table")

	// `archive read <uuid>` — fetch one metadata blob by its UUID and print to stdout.
	archiveReadCmd.Flags().StringVar(&archiveReadDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")

	// `archive body <sha256>` — fetch one file body by its content hash and
	// print to stdout. Use `archive path <sha256>` for the on-disk path.
	archiveBodyCmd.Flags().StringVar(&archiveBodyDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")

	// `archive path <id>` — print the absolute local path. Auto-detects
	// metadata blobs (32-char hex UUIDs → blobs/<uuid>.json) and file
	// bodies (64-char hex SHA-256 → blobs/<sha256>.bin). For CourseId/
	// TopicId lookups, use `archive topic --course <id> <topicId>` first
	// to discover the UUID/SHA, then pipe into this command.
	archivePathCmd.Flags().StringVar(&archivePathDir, "dir", archive.DefaultRoot(), "Archive root directory (default: ~/.config/schooltools/archive)")
}

// `archive read` dumps one metadata blob to stdout. The `--out` flag
// from earlier was dropped; callers who want a file copy should use
// `archive path <uuid>` then `cp` (or `archive body <sha>` for files).
var archiveReadDir string

var archiveReadCmd = &cobra.Command{
	Use:   "read <uuid>",
	Short: "Read a single archived metadata blob by UUID; print to stdout",
	Long: "Dump the raw bytes of one metadata blob from the archive. UUIDs are\n" +
		"listed in `archive topic --course <id> <topicId>` (field `current`) and the\n" +
		"per-course index.json. Pretty-prints JSON; prints verbatim otherwise.",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if archiveReadDir == "" {
			archiveReadDir = archive.DefaultRoot()
		}
		blob, err := archive.LoadBlob(archiveReadDir, args[0])
		if err != nil {
			return err
		}
		if blob == nil {
			return fmt.Errorf("no metadata blob found for uuid %s", args[0])
		}
		var parsed any
		if json.Unmarshal(blob, &parsed) == nil {
			out, _ := json.MarshalIndent(parsed, "", "  ")
			fmt.Println(string(out))
			return nil
		}
		fmt.Println(string(blob))
		return nil
	},
}

// --- `archive path <id>` ---
//
// Prints the absolute local path to an archive blob so callers can use it
// in shell pipelines (e.g. `cat "$(schooltools archive path $(...))"`).
// Auto-detects what kind of blob the ID refers to: a 32-char hex string
// is a metadata blob UUID (random 128-bit identifier); a 64-char hex
// string is a SHA-256 file-body hash. Unknown lengths fall through to
// the metadata-blob path because the blob directory is the same for
// both.
var archivePathDir string

var archivePathCmd = &cobra.Command{
	Use:   "path <id>",
	Short: "Print the absolute local path to an archive blob (metadata UUID or body SHA-256)",
	Long: "Print the on-disk location of one archive blob. Use this to feed the path\n" +
		"into other shell commands without first writing through --out.\n\n" +
		"Detection rules:\n" +
		"  • 32-char hex string  → metadata blob at <root>/blobs/<id>.json\n" +
		"  • 64-char hex string  → file body at <root>/blobs/<id>.bin\n" +
		"  • any other length    → treated as a metadata blob (may not exist)\n\n" +
		"To copy a file out of the archive:\n" +
		"  cp \"$(schooltools archive path <sha256>)\" ~/my-folder/\n" +
		"or pipe directly:\n" +
		"  cat \"$(schooltools archive path <sha256>)\" | less",
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if archivePathDir == "" {
			archivePathDir = archive.DefaultRoot()
		}
		abs, err := filepath.Abs(archivePathDir)
		if err != nil {
			return fmt.Errorf("resolve archive dir: %w", err)
		}
		id := strings.TrimSpace(args[0])
		var blobPath string
		switch len(id) {
		case 64:
			// SHA-256 → file body
			blobPath = archive.BodyPath(abs, id)
		default:
			// 32-char UUID → metadata blob
			blobPath = archive.MetadataBlobPath(abs, id)
		}
		// Best-effort existence check: print exists/not alongside the
		// path so callers know whether the lookup was successful.
		if _, statErr := os.Stat(blobPath); statErr != nil {
			if os.IsNotExist(statErr) {
				fmt.Fprintf(os.Stderr, "warning: %s does not exist on disk (path printed anyway)\n", blobPath)
			} else {
				return statErr
			}
		}
		fmt.Println(blobPath)
		return nil
	},
}

// --- `archive body <sha256>` ---

var archiveBodyDir string

var archiveBodyCmd = &cobra.Command{
	Use:   "body <sha256>",
	Short: "Read a single archived file body by SHA-256, print to stdout",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if archiveBodyDir == "" {
			archiveBodyDir = archive.DefaultRoot()
		}
		abs, err := filepath.Abs(archiveBodyDir)
		if err != nil {
			return fmt.Errorf("resolve archive dir: %w", err)
		}
		body, err := archive.LoadFileBody(abs, args[0])
		if err != nil {
			return err
		}
		if body == nil {
			return fmt.Errorf("no body saved for sha256 %s (run `schooltools archive` with --files)", args[0])
		}
		_, err = os.Stdout.Write(body)
		return err
	},
}

// runArchiveDiff previews what the next archive pass would do without
// fetching any topic bodies or writing any state. Same auth + lock as Run.
func runArchiveDiff(args []string) error {
	if archiveDir == "" {
		archiveDir = archive.DefaultRoot()
	}
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return fmt.Errorf("resolve archive dir: %w", err)
	}
	lock, err := archive.TryLock(abs)
	if err != nil {
		if errors.Is(err, archive.ErrAlreadyRunning) {
			fmt.Fprintln(os.Stderr, "Another archive run is in progress; exiting cleanly.")
			return nil
		}
		return fmt.Errorf("acquire archive lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     archiveEnvFile,
		AutoRefresh: archiveAutoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}

	start := time.Now()
	result, err := archive.Diff(archive.Options{
		Root:      abs,
		Jar:       ens.Jar,
		Verbose:   archiveVerbose,
		Quiet:     archiveJSON,
		CourseIDs: args,
	})
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"root":             result.Root,
			"courseIds":        args,
			"allCourses":       len(args) == 0,
			"coursesScanned":   result.CoursesScanned,
			"topicsChecked":    result.TopicsChecked,
			"topicsWouldFetch": result.TopicsWouldFetch,
			"topicsUnchanged":  result.TopicsStale,
			"elapsedSeconds":   elapsed.Seconds(),
			"perCourse":        result.PerCourse,
		}, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	var out string
	rows := make([][]string, 0, len(result.PerCourse))
	for _, c := range result.PerCourse {
		var fetchIDs string
		if len(c.WouldFetchIDs) > 0 {
			fetchIDs = fmt.Sprintf("%d topics", len(c.WouldFetchIDs))
		} else {
			fetchIDs = "—"
		}
		rows = append(rows, []string{
			c.CourseID,
			c.CourseCode,
			c.CourseName,
			fmt.Sprintf("%d", c.TopicsTotal),
			fmt.Sprintf("%d", c.TopicsNew),
			fmt.Sprintf("%d", c.TopicsModified),
			fmt.Sprintf("%d", c.TopicsUnchanged),
			fetchIDs,
		})
	}
	if len(rows) > 0 {
		out = table.Render(table.Options{
			Headers: []string{"ID", "Code", "Name", "Total", "New", "Modified", "Unchanged", "WouldFetch"},
			Widths: table.Widths([]table.ColumnSpec{
				table.Fixed(8), table.Fixed(10), table.Flex(24),
				table.Fixed(6), table.Fixed(5), table.Fixed(9), table.Fixed(10), table.Flex(8),
			}, table.TerminalWidth()),
			Rows: rows,
		})
	} else {
		out = "(no courses matched)\n"
	}
	fmt.Print(out)

	fmt.Printf("\nWould scan %d course%s in %s\n",
		result.CoursesScanned, plural(result.CoursesScanned), elapsed.Round(time.Millisecond))
	fmt.Printf("Topics: %d checked, %d would fetch, %d unchanged\n",
		result.TopicsChecked, result.TopicsWouldFetch, result.TopicsStale)
	if result.TopicsWouldFetch == 0 {
		fmt.Println("Nothing to do — your archive is up to date.")
	} else {
		fmt.Printf("Run `schooltools archive` (without --diff) to fetch these.\n")
	}
	return nil
}

// runArchive does the actual fetch pass.
func runArchive(args []string) error {
	if archiveDir == "" {
		archiveDir = archive.DefaultRoot()
	}
	abs, err := filepath.Abs(archiveDir)
	if err != nil {
		return fmt.Errorf("resolve archive dir: %w", err)
	}

	// Guard against two archive runs racing (timer + manual invocation).
	lock, err := archive.TryLock(abs)
	if err != nil {
		if errors.Is(err, archive.ErrAlreadyRunning) {
			_ = authlog.Log("archive.skipped", map[string]any{"reason": "lock_held", "root": abs})
			fmt.Fprintln(os.Stderr, "Another archive run is in progress; exiting cleanly.")
			return nil
		}
		return fmt.Errorf("acquire archive lock: %w", err)
	}
	defer func() { _ = lock.Release() }()

	ens, err := session.EnsureSession(session.EnsureOptions{
		EnvFile:     archiveEnvFile,
		AutoRefresh: archiveAutoRefresh,
		KMSI:        true,
		LoginFn:     loginFn,
	})
	if err != nil {
		return err
	}

	var hud *ui.Progress
	if !archiveNoSpinner && !archiveJSON {
		hud = ui.NewProgress(ui.ProgressOptions{Title: archiveTitle(args)})
		hud.Start()
		defer hud.Stop()
	}

	start := time.Now()
	result, err := archive.Run(archive.Options{
		Root:          abs,
		Jar:           ens.Jar,
		Verbose:       archiveVerbose,
		Quiet:         archiveJSON,
		CourseIDs:     args,
		Progress:      progressAdapter(hud),
		DownloadFiles: archiveFiles,
	})
	if err != nil {
		return err
	}
	elapsed := time.Since(start)

	if archiveJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"root":           result.Root,
			"courseIds":      args,
			"allCourses":     len(args) == 0,
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
	if archiveFiles {
		fmt.Printf("File bodies: %d new, %d unchanged (same SHA), %d errors\n",
			result.BodiesFetched, result.BodiesReused, result.BodiesFailed)
	}
	return nil
}

// progressAdapter wires the package's ProgressEvent stream to the HUD. The
// adapter is nil-safe: when hud is nil we return nil so the archive package
// skips the callback entirely.
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

// archiveTitle builds the HUD title line based on whether a subset of
// courses was requested.
func archiveTitle(args []string) string {
	if len(args) == 0 {
		return "Archiving all courses"
	}
	if len(args) == 1 {
		return fmt.Sprintf("Archiving course %s", args[0])
	}
	return fmt.Sprintf("Archiving %d courses", len(args))
}

// runArchiveList prints the saved index without hitting the API. Useful for
// cronjobs that want to verify a previous archive run completed. The
// `dir` and `json` values are read from the active subcommand flags so the
// subcommand form (`archive list`) can override them.
func runArchiveList() error {
	// When called as a subcommand the global archiveCmd's --dir/--json are
	// not set; the archiveListCmd owns its own copies. Prefer those.
	dir := archiveListDir
	jsonOut := archiveListJSON
	if dir == "" {
		dir = archive.DefaultRoot()
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve archive dir: %w", err)
	}
	idx, err := archive.LoadIndex(abs)
	if err != nil {
		return err
	}
	ids := archive.SortedCourseIDs(idx)
	if len(ids) == 0 {
		fmt.Printf("No archive at %s yet.\n", abs)
		return nil
	}

	if jsonOut {
		out, _ := json.MarshalIndent(idx, "", "  ")
		fmt.Println(string(out))
		return nil
	}

	fmt.Printf("Archive index at %s (last updated %s)\n", abs, idx.UpdatedAt.Format(time.RFC3339))
	rows := make([][]string, len(ids))
	for i, id := range ids {
		e := idx.Courses[id]
		stamped := "—"
		if !e.TocFetchedAt.IsZero() {
			stamped = e.TocFetchedAt.Format("2006-01-02 15:04")
		}
		_, docErr := os.Stat(filepath.Join(abs, "courses", id, "index.json"))
		docMark := "○"
		if docErr == nil {
			docMark = "●"
		}
		rows[i] = []string{
			e.OrgUnitId,
			activeMark(e.IsActive),
			e.Code,
			e.Name,
			fmt.Sprintf("%d", e.TopicsTotal),
			fmt.Sprintf("%d", e.TopicsNew),
			fmt.Sprintf("%d", e.TopicsStale),
			stamped,
			docMark,
		}
	}
	out := table.Render(table.Options{
		Headers: []string{"ID", "Active", "Code", "Name", "Total", "New", "Stale", "TOC Fetched", "Indexed"},
		Widths:  table.Widths([]table.ColumnSpec{table.Fixed(8), table.Fixed(6), table.Fixed(10), table.Flex(24), table.Fixed(6), table.Fixed(5), table.Fixed(6), table.Fixed(18), table.Fixed(8)}, table.TerminalWidth()),
		Rows:    rows,
	})
	fmt.Print(out)
	return nil
}
