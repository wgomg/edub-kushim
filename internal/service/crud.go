package service

import (
	"io"
	"reflect"
)

type CrudServices struct {
	Batch        *Batch
	Tag          *Tag
	People       *People
	PeopleType   *PeopleType
	DocumentType *DocumentType
	User         *User
	Orphaned     *Orphaned
	ErroredFiles *ErroredFiles
	ReEnrich     *ReEnrich
	Trash        *TrashService
}

func (s *CrudServices) Close() {
	v := reflect.ValueOf(s).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f := v.Field(i)
		if closer, ok := f.Interface().(io.Closer); ok && !f.IsNil() {
			closer.Close()
		}
	}
}