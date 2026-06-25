package service

type CreateResult[T any] struct {
	Entity T
	Status CreateStatus
}

type UpdateResult[T any] struct {
	Entity T
	Status UpdateStatus
}

type DeleteResult struct {
	ID     int64
	Status DeleteStatus
}
