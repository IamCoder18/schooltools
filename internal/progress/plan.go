package progress

import "time"

// Mark modes for Plan.MarkUnvisited.
const (
	MarkVisited   = "visited"
	MarkCompleted = "completed"
)

// PlanOptions describes the bulk mutation rules. The zero value does
// nothing: MarkUnvisited "" is off, MinVisits 0 is off, SetVisits nil is
// off.
type PlanOptions struct {
	MarkUnvisited string
	MinVisits     int
	SetVisits     *int
}

// Change is one topic's planned or applied write.
type Change struct {
	Action   string       `json:"action"`
	CourseID string       `json:"courseId,omitempty"`
	TopicID  int          `json:"topicId,omitempty"`
	Title    string       `json:"title,omitempty"`
	Before   UserProgress `json:"before"`
	After    UserProgress `json:"after"`
}

// Plan computes the desired record for one topic. An empty action means
// nothing would change and no write should be sent.
//
// Field semantics:
//   - mark visited:   Visited=true, IsRead=true, NumVisits=max(1, current)
//   - mark completed: as visited, plus Completed=true and CompletedDate set
//   - set visits:     NumVisits=N exactly (raising or lowering), Visited=N>0
//   - min visits:     NumVisits=max(current, N), Visited=true when raised
//
// Every other field keeps its current value, so a write never lowers
// TotalTime and never disturbs an existing CompletedDate.
func Plan(cur UserProgress, opts PlanOptions, now time.Time) Change {
	after := cur
	actions := []string{}

	if isUnvisited(cur) {
		switch opts.MarkUnvisited {
		case MarkVisited:
			after = markVisited(after)
			actions = append(actions, "mark-visited")
		case MarkCompleted:
			after = markVisited(after)
			after.Completed = true
			after.CompletedDate = isoPtr(now)
			actions = append(actions, "mark-completed")
		}
	}
	if opts.SetVisits != nil && after.NumVisits != *opts.SetVisits {
		after.NumVisits = *opts.SetVisits
		after.Visited = after.NumVisits > 0
		actions = append(actions, "set-visits")
	} else if opts.MinVisits > 0 && after.NumVisits < opts.MinVisits {
		after.NumVisits = opts.MinVisits
		after.Visited = true
		actions = append(actions, "min-visits")
	}

	if len(actions) == 0 || Equal(cur, after) {
		return Change{Before: cur, After: cur}
	}
	return Change{Action: joinActions(actions), Before: cur, After: after}
}

// PlanDone computes a mark-done (or --undo) write for one topic. Done sets
// Completed and CompletedDate (backdating is honored by the server) and
// treats the topic as opened once; undo clears Completed and CompletedDate
// and touches nothing else.
func PlanDone(cur UserProgress, date string, undo bool, now time.Time) Change {
	after := cur
	action := ""
	if undo {
		if cur.Completed || cur.CompletedDate != nil {
			after.Completed = false
			after.CompletedDate = nil
			action = "undo-done"
		}
	} else if !cur.Completed || cur.CompletedDate == nil || date != "" {
		after = markVisited(after)
		after.Completed = true
		if date != "" {
			after.CompletedDate = &date
		} else {
			after.CompletedDate = isoPtr(now)
		}
		action = "mark-done"
	}
	if action == "" || Equal(cur, after) {
		return Change{Before: cur, After: cur}
	}
	return Change{Action: action, Before: cur, After: after}
}

// PlanViewed computes a visits/time write for one topic. SetVisits, when
// non-nil, forces the visit count exactly (downward included); MinVisits >
// 0 only raises topics below N; wantTime adds that many seconds to
// TotalTime as a target the server may clamp. Visited tracks NumVisits > 0.
func PlanViewed(cur UserProgress, setVisits *int, minVisits, wantTime int) Change {
	after := cur
	actions := []string{}
	if setVisits != nil && after.NumVisits != *setVisits {
		after.NumVisits = *setVisits
		after.Visited = after.NumVisits > 0
		actions = append(actions, "set-visits")
	} else if minVisits > 0 && after.NumVisits < minVisits {
		after.NumVisits = minVisits
		after.Visited = true
		actions = append(actions, "min-visits")
	}
	if wantTime > 0 {
		after.TotalTime = cur.TotalTime + wantTime
		actions = append(actions, "add-time")
	}
	if len(actions) == 0 || Equal(cur, after) {
		return Change{Before: cur, After: cur}
	}
	return Change{Action: joinActions(actions), Before: cur, After: after}
}

// Equal compares two records field by field, dereferencing the optional
// timestamps so pointer identity never counts as a change.
func Equal(a, b UserProgress) bool {
	return a.ObjectId == b.ObjectId &&
		a.UserId == b.UserId &&
		a.IsRead == b.IsRead &&
		a.NumVisits == b.NumVisits &&
		a.TotalTime == b.TotalTime &&
		a.Visited == b.Visited &&
		a.Completed == b.Completed &&
		a.IsExempt == b.IsExempt &&
		strPtrEqual(a.CompletedDate, b.CompletedDate) &&
		strPtrEqual(a.LastVisited, b.LastVisited)
}

// IsUnvisited reports whether a record counts as never meaningfully
// visited: not flagged visited, or zero recorded visits.
func IsUnvisited(p UserProgress) bool {
	return !p.Visited || p.NumVisits == 0
}

func isUnvisited(p UserProgress) bool { return IsUnvisited(p) }

func markVisited(p UserProgress) UserProgress {
	p.Visited = true
	p.IsRead = true
	if p.NumVisits < 1 {
		p.NumVisits = 1
	}
	return p
}

func isoPtr(t time.Time) *string {
	s := t.UTC().Format("2006-01-02T15:04:05.000Z")
	return &s
}

func strPtrEqual(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func joinActions(actions []string) string {
	out := ""
	for i, a := range actions {
		if i > 0 {
			out += "+"
		}
		out += a
	}
	return out
}
