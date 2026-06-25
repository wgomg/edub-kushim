package service

type CreateStatus int

const (
	Created CreateStatus = iota
	Conflict
	Invalid
)

type UpdateStatus int

const (
	Updated UpdateStatus = iota
	UpdateConflict
	UpdateNotFound
	UpdateInvalid
	Noop
)

type DeleteStatus int

const (
	Deleted DeleteStatus = iota
	DeleteNotFound
	DeleteConflict
)
