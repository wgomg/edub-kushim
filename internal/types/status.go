package types

// CreateStatus is a create outcome: created, conflicting with an
// existing name, invalid input, applied (existing store name resolved),
// or aliased onto a canonical.
type CreateStatus int

// The create outcomes, in iota order.
const (
	Created CreateStatus = iota
	Conflict
	Invalid
	Apply
	Alias
)

// UpdateStatus is an update outcome: updated, name conflict, not found,
// invalid input, or a no-op rename.
type UpdateStatus int

// The update outcomes, in iota order.
const (
	Updated UpdateStatus = iota
	UpdateConflict
	UpdateNotFound
	UpdateInvalid
	Noop
)

// DeleteStatus is a delete outcome: deleted, not found, or conflicting
// with in-use data.
type DeleteStatus int

// The delete outcomes, in iota order.
const (
	Deleted DeleteStatus = iota
	DeleteNotFound
	DeleteConflict
)

// PromoteStatus is a promotion outcome: not found, already promoted, or
// promoted now.
type PromoteStatus int

// The promote outcomes, in iota order.
const (
	PromoteNotFound PromoteStatus = iota
	PromoteAlreadyPromoted
	PromotePromoted
)

// DemoteStatus reports how a demotion request resolved. NotPromoted covers both a tag that was never
// promoted and one already demoted, so repeating a demotion is a no-op rather than a failure; Seeded
// reports a value the exemption keeps out of the demotion paths.
type DemoteStatus int

// The demote outcomes, in iota order.
const (
	DemoteNotFound DemoteStatus = iota
	DemoteNotPromoted
	DemoteSeeded
	DemoteDemoted
)

// FoldStatus reports how an operator fold resolved: the loser name is not a live tag, the fold could
// not apply (its canonical is gone or the alias was already recorded), the loser is operator-owned inventory
// the seeder would resurrect, or it collapsed the names.
type FoldStatus int

// The fold outcomes, in iota order.
const (
	FoldNotFound FoldStatus = iota
	FoldNotApplied
	FoldApplied
	FoldSeeded
)

// CompositionStatus reports how an operator composition decision resolved: the tag is missing, it is
// no longer provisional, it is seeded and cannot be replaced, it changed underneath the decision, the
// decision or its parts are invalid, or it was applied.
type CompositionStatus int

// The composition decision outcomes, in iota order.
const (
	CompositionNotFound CompositionStatus = iota
	CompositionNotProvisional
	CompositionConflict
	CompositionInvalid
	CompositionApplied
	CompositionSeeded
)

// RetireStatus reports how a retire resolved: the tag is missing, still promoted (demote owns that
// case), or removed from the store without blocklisting its name.
type RetireStatus int

// The retire outcomes, in iota order.
const (
	RetireNotFound RetireStatus = iota
	RetirePromoted
	RetireRetired
)

// AcceptStatus reports how an acceptance resolved: the tag is missing, it is already promoted, it was
// already accepted, it is not deferred to review, another review concern still withholds it, or the
// deferral was waived.
type AcceptStatus int

// The acceptance outcomes, in iota order.
const (
	AcceptNotFound AcceptStatus = iota
	AcceptNotProvisional
	AcceptAlready
	AcceptNotDeferred
	AcceptHeld
	AcceptApplied
)

// SeparateStatus reports how an identity separation resolved: the tag is missing, it is already promoted,
// no identity hold withholds it, or the operator's separation cleared the hold.
type SeparateStatus int

// The separation outcomes, in iota order.
const (
	SeparateNotFound SeparateStatus = iota
	SeparateNotProvisional
	SeparateWithoutHold
	SeparateApplied
)
