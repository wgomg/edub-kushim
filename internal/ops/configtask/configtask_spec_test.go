package configtask

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestMigrationSpecsPreserveTheirPayload pins that no migration field drops out of the task payload.
func TestMigrationSpecsPreserveTheirPayload(t *testing.T) {
	dbIn := MigrateDBPayload{
		ConfigDir: "/cfg",
		Host:      "db.example",
		Port:      "5432",
		User:      "edub",
		Password:  "secret",
		Database:  "edub",
		SSLMode:   "disable",
	}
	spec, err := MigrateDBSpec(dbIn)
	testutil.AssertNoError(t, err, "migrate-db spec")
	testutil.AssertEqual(t, spec.Type, types.Task.Type.MigrateDB, "migrate-db task type")
	var dbOut MigrateDBPayload
	if err := json.Unmarshal(spec.Payload, &dbOut); err != nil {
		t.Fatalf("decode migrate-db payload: %v", err)
	}
	if dbOut != dbIn {
		t.Fatalf("payload = %+v, want %+v", dbOut, dbIn)
	}

	storageIn := MigrateStoragePayload{
		ConfigDir:         "/cfg",
		OldStorageDir:     "/old/storage",
		NewStorageDir:     "/new/storage",
		OldConsumptionDir: "/old/inbox",
		NewConsumptionDir: "/new/inbox",
	}
	spec, err = MigrateStorageSpec(storageIn)
	testutil.AssertNoError(t, err, "migrate-storage spec")
	testutil.AssertEqual(t, spec.Type, types.Task.Type.MigrateStorage, "migrate-storage task type")
	var storageOut MigrateStoragePayload
	if err := json.Unmarshal(spec.Payload, &storageOut); err != nil {
		t.Fatalf("decode migrate-storage payload: %v", err)
	}
	if storageOut != storageIn {
		t.Fatalf("payload = %+v, want %+v", storageOut, storageIn)
	}
}
