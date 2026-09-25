package service

import (
	"io"
	"reflect"
)

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
	Trash        *TrashService
}

// Close releases every member service that owns resources.
func (s *CrudServices) Close() {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if closer, ok := f.Interface().(io.Closer); ok && !f.IsNil() {
			_ = closer.Close()
		}
	}
}
