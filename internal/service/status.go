package service

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
