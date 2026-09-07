// Package quiz exposes the per-course quiz surface (PROMOTED).
//
// Plan §5.6 subcommands:
//
//   quiz --course <id>                       list
//   quiz --course <id> get <quizId>          metadata: dates, attempts, time limit
//   quiz --course <id> attempts <quizId>     my attempts
//   quiz --course <id> attempt <quizId> <attemptId>   one attempt
//
// Per D2L_API_RESEARCH.md §4.9, students cannot enumerate quiz questions
// outside an active attempt, so no CLI command exposes question bodies.
package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/aarav/schooltools/internal/dupdata"
	"github.com/aarav/schooltools/internal/session"
	"github.com/aarav/schooltools/internal/ua"
)

var (
	quizCourse  string
	quizEnvFile string
	quizAutoRef bool
	quizJSON    bool
)

type Quiz struct {
	QuizId            json.Number `json:"QuizId"`
	Name              string      `json:"Name"`
	IsActive          bool        `json:"IsActive"`
	StartDate         *string     `json:"StartDate"`
	EndDate           *string     `json:"EndDate"`
	AttemptsAllowed   any         `json:"AttemptsAllowed"`
	TimeLimit         any         `json:"TimeLimit"`
	AvailableDate     *string     `json:"AvailableDate"`
	DueDate           *string     `json:"DueDate"`
	DisplayInCalendar bool        `json:"DisplayInCalendar"`
}

type QuizAttempt struct {
	Id              int    `json:"Id"`
	QuizId          int    `json:"QuizId"`
	UserId          int    `json:"UserId"`
	StartDate       string `json:"StartDate"`
	EndDate         string `json:"EndDate"`
	Score           json.Number `json:"Score"`
	MaxScore        json.Number `json:"MaxScore"`
	AttemptNumber   int    `json:"AttemptNumber"`
	IsCompleted     bool   `json:"IsCompleted"`
}

func init() {
	root := &cobra.Command{
		Use:   "quiz",
		Short: "List quizzes and show my attempts (per-course)",
		Long: "Browse a course's quizzes, view their metadata (dates, attempts\n" +
			"allowed, time limit), and list / inspect your own attempts.\n\n" +
			"Students cannot enumerate quiz questions outside an active attempt\n" +
			"(per D2L research §4.9), so the CLI does not expose question bodies.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return cmd.Help() },
	}
	addQuizFlags := func(c *cobra.Command) {
		c.Flags().StringVar(&quizCourse, "course", "", "Course OrgUnitId (required)")
		c.Flags().StringVarP(&quizEnvFile, "env-file", "e", ".env", "Path to .env for auto-refresh")
		c.Flags().BoolVar(&quizAutoRef, "auto-refresh", false, "Re-run 'login' if the session is expired")
		c.Flags().BoolVar(&quizJSON, "json", false, "Emit raw JSON instead of a table")
	}
	list := &cobra.Command{
		Use: "list", Short: "List quizzes in a course", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizList() },
	}
	get := &cobra.Command{
		Use: "get <quizId>", Short: "Show one quiz's metadata", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizGet(args[0]) },
	}
	atts := &cobra.Command{
		Use: "attempts <quizId>", Short: "List my attempts for a quiz", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizAttempts(args[0]) },
	}
	att := &cobra.Command{
		Use: "attempt <quizId> <attemptId>", Short: "Show one attempt's details", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error { return runQuizAttempt(args[0], args[1]) },
	}
	addQuizFlags(list)
	addQuizFlags(get)
	addQuizFlags(atts)
	addQuizFlags(att)
	root.AddCommand(list, get, atts, att)
	rootCmd.AddCommand(root)
}

func requireQuizCourse() (string, error) {
	if quizCourse == "" {
		return "", fmt.Errorf("--course <orgUnitId> is required (e.g. --course 1527886)")
	}
	return quizCourse, nil
}

func quizSession() (session.EnsureResult, error) {
	return session.EnsureSession(session.EnsureOptions{
		EnvFile:     quizEnvFile,
		AutoRefresh: quizAutoRef,
		KMSI:        true,
		LoginFn:     loginFn,
	})
}

func runQuizList() error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/quizzes/", ua.D2LBase, dupdata.D2LVersion(), courseID)
	items, err := dupdata.FetchJSONList[Quiz](raw, ens.Jar)
	if err != nil {
		return err
	}
	if quizJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No quizzes found for this course.")
		return nil
	}
	fmt.Printf("%d quiz%s\n", len(items), plural(len(items)))
	for _, q := range items {
		when := ""
		if q.AvailableDate != nil {
			when = *q.AvailableDate
		} else if q.StartDate != nil {
			when = *q.StartDate
		}
		if len(when) > 16 {
			when = when[:16]
		}
		active := ""
		if !q.IsActive {
			active = "[inactive] "
		}
		fmt.Printf("  %s\t%s%s\n", q.QuizId.String(), active+when, truncate(q.Name, 60))
	}
	return nil
}

func runQuizGet(quizID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/quizzes/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, quizID)
	var out Quiz
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}

func runQuizAttempts(quizID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/quizzes/%s/attempts/?userId=me",
		ua.D2LBase, dupdata.D2LVersion(), courseID, quizID)
	items, err := dupdata.FetchJSONList[QuizAttempt](raw, ens.Jar)
	if err != nil {
		return err
	}
	if quizJSON {
		b, _ := json.MarshalIndent(items, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	if len(items) == 0 {
		fmt.Println("No attempts found.")
		return nil
	}
	fmt.Printf("%d attempt%s\n", len(items), plural(len(items)))
	for _, a := range items {
		score := a.Score.String()
		if a.MaxScore.String() != "" && a.MaxScore.String() != "0" {
			score = fmt.Sprintf("%s / %s", a.Score.String(), a.MaxScore.String())
		}
		when := a.StartDate
		if len(when) > 16 {
			when = when[:16]
		}
		fmt.Printf("  %d\t#%d\t%s\t%s\n", a.Id, a.AttemptNumber, when, score)
	}
	return nil
}

func runQuizAttempt(quizID, attemptID string) error {
	courseID, err := requireQuizCourse()
	if err != nil {
		return err
	}
	ens, err := quizSession()
	if err != nil {
		return err
	}
	raw := fmt.Sprintf("%s/d2l/api/le/%s/%s/quizzes/%s/attempts/%s",
		ua.D2LBase, dupdata.D2LVersion(), courseID, quizID, attemptID)
	var out QuizAttempt
	if err := dupdata.FetchJSON(raw, ens.Jar, &out); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	return nil
}
