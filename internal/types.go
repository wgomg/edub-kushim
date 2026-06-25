package types

import (
	"io"
	"reflect"

	"github.com/wgomg/edub-kushim/internal/service"
)

type CrudServices struct {
	Tag          *service.Tag
	People       *service.People
	PeopleType   *service.PeopleType
	DocumentType *service.DocumentType
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
