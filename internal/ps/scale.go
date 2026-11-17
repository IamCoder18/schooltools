package ps

import "strings"

// Proficiency is one level of the CBE High School Proficiency Scale.
// Percent is the nominal percentage CBE publishes for the level. It is
// nil for levels whose percentage CBE does not publish, so callers can
// distinguish "unknown" from a genuine zero.
type Proficiency struct {
	Code    string `json:"code"`
	Label   string `json:"label"`
	Percent *int   `json:"percent,omitempty"`
}

func intPtr(v int) *int { return &v }

var proficiencyScale = []Proficiency{
	{Code: "EX2", Label: "Exemplary 2", Percent: intPtr(100)},
	{Code: "EX1", Label: "Exemplary 1", Percent: intPtr(95)},
	{Code: "PR2", Label: "Proficient 2", Percent: intPtr(85)},
	{Code: "PR1", Label: "Proficient 1", Percent: intPtr(75)},
	{Code: "DV2", Label: "Developing 2"},
	{Code: "DV1", Label: "Developing 1"},
	{Code: "BG2", Label: "Beginning 2"},
	{Code: "BG1", Label: "Beginning 1"},
}

// Scale returns a copy of the CBE High School Proficiency Scale,
// ordered from highest to lowest. Callers may mutate the result without
// affecting the package-level table.
func Scale() []Proficiency {
	out := make([]Proficiency, len(proficiencyScale))
	for i, p := range proficiencyScale {
		out[i] = p
		if p.Percent != nil {
			v := *p.Percent
			out[i].Percent = &v
		}
	}
	return out
}

// LookupProficiency resolves a PowerSchool score token such as "EX1" to
// its scale entry. Matching is case-insensitive and ignores surrounding
// whitespace. The second result is false for unknown or empty tokens.
func LookupProficiency(code string) (Proficiency, bool) {
	needle := strings.ToUpper(strings.TrimSpace(code))
	if needle == "" {
		return Proficiency{}, false
	}
	for _, p := range proficiencyScale {
		if p.Code == needle {
			return p, true
		}
	}
	return Proficiency{}, false
}
