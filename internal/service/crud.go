package service

import "errors"

// CrudServices is the composition of the use-case services the API and
// the CLI share; the tag services live in tags/curation since stage 5.
type CrudServices struct {
	Batch        *Batch
	People       *People
	PeopleType   *PeopleType
	DocumentType *DocumentType
	User         *User
	Orphaned     *Orphaned
	ErroredFiles *ErroredFiles
	ReEnrich     *ReEnrich
	Trash        *Trash
}

// Close releases every member service that owns resources and returns the
// joined close failures. The only closeable member today, Trash, cannot
// fail to close, so the returned error is always nil for now; the error
// return is kept so a future failing member reaches the composition root.
func (s *CrudServices) Close() error {
	var errs []error
	if s.Trash != nil {
		errs = append(errs, s.Trash.Close())
	}
	return errors.Join(errs...)
}
