package cmd

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/IamCoder18/schooltools/internal/ps"
	"github.com/IamCoder18/schooltools/internal/table"
)

var (
	sisAssignmentsYear    int
	sisAssignmentsSection int64

	sisAssignmentsMaxCells int
)

// sisAssignment is the flattened row we surface to the user. PowerSchool
// stores assignments keyed by (student, section, standard) and only one
// assignment can live in each cell, but the legacy
// /guardian/studentSectionStemAssignments.json endpoint returns the
// human-friendly fields (assignmentName, category, dueDate, score
// token) so we don't need to fetch /ws/xte/assignment/lookup at all.
type sisAssignment struct {
	SectionDCID    int64    `json:"section_dcid"`
	SectionName    string   `json:"section_name"`
	StandardID     int64    `json:"standard_id"`
	AssignmentID   int64    `json:"assignment_id"`
	AssignmentName string   `json:"assignment_name"`
	Category       string   `json:"category"`
	DueDate        string   `json:"due_date"`
	Score          string   `json:"score"`
	ScoreLabel     string   `json:"score_label,omitempty"`
	ScorePercent   *float64 `json:"score_percent,omitempty"`
	IsCollected    bool     `json:"is_collected"`
	IsLate         bool     `json:"is_late"`
	IsMissing      bool     `json:"is_missing"`
	IsExempt       bool     `json:"is_exempt"`
	IsAbsent       bool     `json:"is_absent"`
	IsIncomplete   bool     `json:"is_incomplete"`
}

const (
	// sisCellWorkers bounds concurrent legacy-endpoint calls. PowerSchool
	// issues session cookies with no Expires and may rate-limit a student
	// account, so the fan-out is deliberately capped rather than opened
	// to one goroutine per cell.
	sisCellWorkers = 6
	// sisMaxCells is the default ceiling on per-cell requests. A student
	// with ~13 sections and ~5 standards each is ~65 cells, so the default
	// covers a normal full run while stopping a mistyped --section from
	// turning into hundreds of live calls.
	sisMaxCells = 200
)

// sisCell is one (section, standard) pair to drill into.
type sisCell struct {
	sectionDCID int64
	standardID  int64
}

// fetchStemCells drills into the legacy per-cell endpoint with bounded
// concurrency. Per-cell failures are collected rather than aborting the
// run, so one flaky cell does not discard every other assignment.
func fetchStemCells(client *ps.Client, studentID int64, cells []sisCell) ([]sisAssignment, map[sisCell]error) {
	failures := make(map[sisCell]error)
	results := make([][]sisAssignment, len(cells))
	var failMu sync.Mutex

	work := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < sisCellWorkers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				c := cells[i]
				cell, err := client.GetStemAssignment(studentID, c.sectionDCID, c.standardID)
				if err != nil {
					failMu.Lock()
					failures[c] = err
					failMu.Unlock()
					continue
				}
				results[i] = stemRows(cell.Data)
			}
		}()
	}
	for i := range cells {
		work <- i
	}
	close(work)
	wg.Wait()

	var out []sisAssignment
	for _, rs := range results {
		out = append(out, rs...)
	}
	return out, failures
}

// stemRows converts one cell's payload into assignment rows, dropping
// the sentinel empty cell the endpoint returns when no assignment has
// been posted.
func stemRows(data []ps.StemAssignment) []sisAssignment {
	var out []sisAssignment
	for _, d := range data {
		if d.AssignmentID == "" {
			continue
		}
		id, _ := strconv.ParseInt(d.AssignmentID, 10, 64)
		sec, _ := strconv.ParseInt(d.SectionDCID, 10, 64)
		stdid, _ := strconv.ParseInt(d.StandardID, 10, 64)
		label, percent := proficiencyFor(d.ActualScoreEntered)
		out = append(out, sisAssignment{
			SectionDCID:    sec,
			StandardID:     stdid,
			AssignmentID:   id,
			AssignmentName: html.UnescapeString(d.AssignmentName),
			Category:       html.UnescapeString(d.Category),
			DueDate:        d.DueDate,
			Score:          d.ActualScoreEntered,
			ScoreLabel:     label,
			ScorePercent:   percent,
			IsCollected:    d.IsCollected == "1",
			IsLate:         d.IsLate == "1",
			IsMissing:      d.IsMissing == "1",
			IsExempt:       d.IsExempt == "1",
			IsAbsent:       d.IsAbsent == "1",
			IsIncomplete:   d.IsIncomplete == "1",
		})
	}
	return out
}

// proficiencyFor maps a PowerSchool score token to its human label and
// nominal percentage. Unknown or empty tokens return empty values.
func proficiencyFor(code string) (string, *float64) {
	p, ok := ps.LookupProficiency(code)
	if !ok {
		return "", nil
	}
	if p.Percent == nil {
		return p.Label, nil
	}
	v := float64(*p.Percent)
	return p.Label, &v
}

func formatScore(code, label string) string {
	if code == "" {
		return "—"
	}
	if label == "" {
		return code
	}
	return code + " (" + label + ")"
}

func assignmentFlags(a sisAssignment) string {
	var flags []string
	if a.IsCollected {
		flags = append(flags, "collected")
	}
	if a.IsLate {
		flags = append(flags, "late")
	}
	if a.IsMissing {
		flags = append(flags, "missing")
	}
	if a.IsExempt {
		flags = append(flags, "exempt")
	}
	if a.IsAbsent {
		flags = append(flags, "absent")
	}
	if a.IsIncomplete {
		flags = append(flags, "incomplete")
	}
	return strings.Join(flags, ",")
}

var sisAssignmentsCmd = &cobra.Command{
	Use:   "assignments",
	Short: "List assignments posted to the student's sections",
	Long: "Walks every (student, section, standard) tuple from\n" +
		"/ws/xte/standard_grade_section and drills into the per-cell\n" +
		"/guardian/studentSectionStemAssignments.json endpoint to surface\n" +
		"every assignment a teacher has posted. PowerSchool stores each\n" +
		"assignment against a specific standard, so this command makes\n" +
		"one API call per standard — pass --section to limit to a single\n" +
		"class and reduce the number of calls.",
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
		yearID, err := resolveYearID(client, dcid, sisAssignmentsYear)
		if err != nil {
			return err
		}
		stu, err := client.GetEnrollments(dcid, yearID)
		if err != nil {
			return err
		}
		// Build a sectionDCID → course name map so we can label each row.
		sectionName := map[int64]string{}
		sectionIDs := make([]int64, 0, len(stu.Enrollments))
		for _, e := range stu.Enrollments {
			if sisAssignmentsSection == 0 || e.SectionDCID == sisAssignmentsSection {
				sectionIDs = append(sectionIDs, e.SectionDCID)
				sectionName[e.SectionDCID] = e.Section.CourseName
			}
		}
		if len(sectionIDs) == 0 {
			if sisAssignmentsSection != 0 {
				return fmt.Errorf("section %d is not among this student's enrollments for year %d", sisAssignmentsSection, yearID)
			}
			return fmt.Errorf("no enrollments for school year %d; pass --year <id> to select a different year", yearID)
		}
		// Get the (student, section, standard) join — one row per standard.
		rows, err := client.GetStandardGrades(dcid, sectionIDs, yearID)
		if err != nil {
			return err
		}
		cells := make([]sisCell, 0, len(rows))
		for _, r := range rows {
			cells = append(cells, sisCell{sectionDCID: r.SectionDCID, standardID: r.StandardID})
		}
		if sisAssignmentsMaxCells > 0 && len(cells) > sisAssignmentsMaxCells {
			return fmt.Errorf("%d standard cells exceed --max-cells %d; narrow the run with --section <dcid> or raise --max-cells", len(cells), sisAssignmentsMaxCells)
		}

		fmt.Fprintf(os.Stderr, "Fetching %d standard cell(s) across %d section(s) (year %d)…\n", len(cells), len(sectionIDs), yearID)
		fetched, failures := fetchStemCells(client, stu.ID, cells)

		// De-dup on (section, assignment); assignmentId can recur.
		seen := map[string]bool{}
		out := make([]sisAssignment, 0, len(fetched))
		for _, a := range fetched {
			a.SectionName = sectionName[a.SectionDCID]
			key := fmt.Sprintf("%d|%d", a.SectionDCID, a.AssignmentID)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, a)
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].SectionName != out[j].SectionName {
				return out[i].SectionName < out[j].SectionName
			}
			if out[i].DueDate != out[j].DueDate {
				return out[i].DueDate < out[j].DueDate
			}
			return out[i].AssignmentID < out[j].AssignmentID
		})

		if len(failures) > 0 {
			keys := make([]string, 0, len(failures))
			for c := range failures {
				keys = append(keys, fmt.Sprintf("section %d/standard %d", c.sectionDCID, c.standardID))
			}
			sort.Strings(keys)
			shown := keys
			if len(shown) > 5 {
				shown = shown[:5]
			}
			fmt.Fprintf(os.Stderr, "Warning: %d of %d cell(s) failed to load and are missing from the results: %s\n", len(failures), len(cells), strings.Join(shown, ", "))
			if len(keys) > len(shown) {
				fmt.Fprintf(os.Stderr, "  …and %d more.\n", len(keys)-len(shown))
			}
		}

		if asJSON {
			j := map[string]any{
				"count":        len(out),
				"year_id":      yearID,
				"section_ids":  sectionIDs,
				"cells_total":  len(cells),
				"cells_failed": len(failures),
				"assignments":  out,
			}
			b, _ := json.MarshalIndent(j, "", "  ")
			fmt.Println(string(b))
			return nil
		}
		if len(out) == 0 {
			fmt.Fprintln(os.Stderr, "No assignments posted yet (PowerSchool returns empty cells until teachers grade).")
			return nil
		}
		fmt.Fprintf(os.Stderr, "Found %d assignment(s) across %d section(s) (year %d)\n", len(out), len(sectionIDs), yearID)
		trows := make([][]string, len(out))
		for i, a := range out {
			name := a.AssignmentName
			if flags := assignmentFlags(a); flags != "" {
				name += " [" + flags + "]"
			}
			trows[i] = []string{
				a.SectionName,
				a.DueDate,
				a.Category,
				name,
				formatScore(a.Score, a.ScoreLabel),
			}
		}
		fmt.Print(table.Render(table.Options{
			Headers: []string{"Course", "Due", "Category", "Assignment", "Score"},
			Widths: table.Widths(
				[]table.ColumnSpec{
					table.Fixed(20), table.Fixed(13), table.Fixed(18),
					table.Flex(28), table.Fixed(18),
				},
				table.TerminalWidth()),
			Rows: trows,
			Wrap: true,
		}))
		return nil
	},
}

func init() {
	sisAssignmentsCmd.Flags().IntVar(&sisAssignmentsYear, "year", 0, "School year ID (default: latest from the year picker)")
	sisAssignmentsCmd.Flags().Int64Var(&sisAssignmentsSection, "section", 0, "Limit to a single section DCID (default: every enrolled section)")
	sisAssignmentsCmd.Flags().IntVar(&sisAssignmentsMaxCells, "max-cells", sisMaxCells, "Maximum per-standard API calls before aborting (0 disables the limit)")
	sisCmd.AddCommand(sisAssignmentsCmd)
}
