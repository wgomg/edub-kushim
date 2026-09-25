package service

// CreateResult pairs one entity with the outcome of its create request.
type CreateResult[T any] struct {
	Entity T
	Status CreateStatus
}

// UpdateResult pairs one entity with the outcome of its update.
type UpdateResult[T any] struct {
	Entity T
	Status UpdateStatus
}

// DeleteResult pairs a deleted id with the delete outcome.
type DeleteResult struct {
	ID     int64
	Status DeleteStatus
}
