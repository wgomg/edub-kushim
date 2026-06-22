package types

import (
	"io"
	"reflect"

	"github.com/wgomg/edub-kushim/internal/documenttypes"
	"github.com/wgomg/edub-kushim/internal/people"
	"github.com/wgomg/edub-kushim/internal/tags"
)

type CrudServices struct {
	Tag          *tags.TagService
	People       *people.PeopleService
	PeopleType   *people.PeopleTypeService
	DocumentType *documenttypes.DocumentTypeService
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
