package service

import "testing"

func TestCrudServicesClose_ToleratesMissingAndUnstartedTrash(t *testing.T) {
	if err := (&CrudServices{}).Close(); err != nil {
		t.Fatalf("close without a trash member: %v", err)
	}
	if err := (&CrudServices{Trash: &Trash{}}).Close(); err != nil {
		t.Fatalf("close an unstarted trash member: %v", err)
	}
}

func TestCrudServicesClose_StopsBackgroundPurge(t *testing.T) {
	trash := &Trash{}
	trash.StartBackgroundPurge()

	s := &CrudServices{Trash: trash}
	if err := s.Close(); err != nil {
		t.Fatalf("close with a trash member: %v", err)
	}
	if trash.ctx.Err() == nil {
		t.Fatal("Close must stop the background purge")
	}
}
