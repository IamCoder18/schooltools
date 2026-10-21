package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitFeedbackEntity(t *testing.T) {
	cases := []struct {
		name         string
		args         []string
		wantType     string
		wantEntityID string
	}{
		{name: "no extras", args: nil, wantType: "user", wantEntityID: "me"},
		{name: "folder only", args: []string{"2308269"}, wantType: "user", wantEntityID: "me"},
		{name: "me in type slot falls back to own id",
			args: []string{"2308269", "me"}, wantType: "user", wantEntityID: "me"},
		{name: "me is case-insensitive",
			args: []string{"2308269", "ME"}, wantType: "user", wantEntityID: "me"},
		{name: "explicit user keeps me default",
			args: []string{"2308269", "user"}, wantType: "user", wantEntityID: "me"},
		{name: "user with id", args: []string{"2308269", "user", "782002"}, wantType: "user", wantEntityID: "782002"},
		{name: "group with id", args: []string{"2308269", "group", "1042"}, wantType: "group", wantEntityID: "1042"},
		{name: "me then explicit id", args: []string{"2308269", "me", "782002"}, wantType: "user", wantEntityID: "782002"},
	}
	for _, tc := range cases {
		gotType, gotEntityID := splitFeedbackEntity(tc.args)
		if gotType != tc.wantType || gotEntityID != tc.wantEntityID {
			t.Errorf("%s: splitFeedbackEntity(%v) = (%q, %q), want (%q, %q)",
				tc.name, tc.args, gotType, gotEntityID, tc.wantType, tc.wantEntityID)
		}
	}
}

func TestFeedbackDecodeAndFormat(t *testing.T) {
	sample := `{
		"Files": [],
		"Links": [],
		"Score": null,
		"Feedback": {"Text": "Great!", "Html": "<p>Great!</p>"},
		"GradedSymbol": null,
		"IsGraded": true,
		"RubricAssessments": [{
			"RubricId": 347770,
			"OverallScore": 13.000000000,
			"OverallFeedback": {"Text": "", "Html": ""},
			"CriteriaOutcome": [
				{"CriterionId": 1141660, "LevelId": 3406097, "Score": 2.000000000, "Feedback": {"Text": "", "Html": ""}},
				{"CriterionId": 1141663, "LevelId": 3406098, "Score": 1.000000000,
				 "Feedback": {"Text": "Outline/lines need to be continuous instead of \"scratchy\"", "Html": ""}}
			]
		}]
	}`
	var out Feedback
	if err := json.Unmarshal([]byte(sample), &out); err != nil {
		t.Fatalf("decode sample feedback: %v", err)
	}
	if out.Score != "" {
		t.Errorf("Score null should decode to empty json.Number, got %q", out.Score)
	}
	if got := out.Feedback.Text; got != "Great!" {
		t.Errorf("Feedback.Text = %q, want %q", got, "Great!")
	}
	if !out.IsGraded {
		t.Error("IsGraded = false, want true")
	}
	if len(out.RubricAssessments) != 1 {
		t.Fatalf("len(RubricAssessments) = %d, want 1", len(out.RubricAssessments))
	}
	r := out.RubricAssessments[0]
	if want := json.Number("13.000000000"); r.OverallScore != want {
		t.Errorf("OverallScore = %q, want %q", r.OverallScore, want)
	}
	if len(r.CriteriaOutcome) != 2 {
		t.Fatalf("len(CriteriaOutcome) = %d, want 2", len(r.CriteriaOutcome))
	}

	formatted := formatFeedback(out)
	for _, want := range []string{
		"Feedback:",
		"Status: graded",
		"Comment: Great!",
		"Rubric 347770 overall: 13",
		"criterion 1141660: 2",
		"criterion 1141663: 1 — Outline/lines need to be continuous instead of \"scratchy\"",
	} {
		if !strings.Contains(formatted, want) {
			t.Errorf("formatFeedback missing %q\nGot:\n%s", want, formatted)
		}
	}
	if strings.Contains(formatted, "Grade symbol") {
		t.Errorf("GradedSymbol null must not print a grade-symbol line:\n%s", formatted)
	}
	if strings.Contains(formatted, "13.000000000") {
		t.Errorf("scores must be trimmed for display:\n%s", formatted)
	}
}

func TestTrimJSONNumber(t *testing.T) {
	cases := []struct {
		in   json.Number
		want string
	}{
		{json.Number("13"), "13"},
		{json.Number("13.000000000"), "13"},
		{json.Number("1.500000000"), "1.5"},
		{json.Number("0.100"), "0.1"},
		{json.Number("1.0e3"), "1.0e3"},
	}
	for _, tc := range cases {
		if got := trimJSONNumber(tc.in); got != tc.want {
			t.Errorf("trimJSONNumber(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
