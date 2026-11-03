package tags

// RejudgeScope names one half of an operator re-judge: Quality re-runs the admission judge over the
// value's first live document, and Composition re-runs the split judge over the compound. The strings
// are the task payload's scope vocabulary, so the enqueue path and the kushim handler share them.
type RejudgeScope string

// The re-judge scopes: Quality re-runs the admission judge over the value's first live document, and
// Composition re-runs the split judge over the compound.
const (
	RejudgeScopeQuality     RejudgeScope = "quality"
	RejudgeScopeComposition RejudgeScope = "composition"
)

// Valid reports whether s is one of the declared scopes.
func (s RejudgeScope) Valid() bool {
	return s == RejudgeScopeQuality || s == RejudgeScopeComposition
}
