package configtask

import (
	"encoding/json"
	"testing"

	"github.com/wgomg/edub-kushim/internal/testutil"
	"github.com/wgomg/edub-kushim/internal/types"
)

// TestConfigSpecsDeriveTheKeysTheHandlerLooksUp pins the config payload and dedup key contract.
func TestConfigSpecsDeriveTheKeysTheHandlerLooksUp(t *testing.T) {
	tessdata, err := TessdataSpec("/cfg", "eng")
	testutil.AssertNoError(t, err, "tessdata spec")
	testutil.AssertEqual(t, tessdata.Type, types.Task.Type.Config, "tessdata task type")

	var payload configPayload
	if err := json.Unmarshal(tessdata.Payload, &payload); err != nil {
		t.Fatalf("decode tessdata payload: %v", err)
	}
	testutil.AssertEqual(t, payload.ConfigDir, "/cfg", "config dir")
	testutil.AssertEqual(t, payload.Op, OpTessdata, "operation")
	testutil.AssertEqual(t, payload.Lang, "eng", "language")
	if got := ConfigDedupKey(tessdata.Payload); got != DedupKeyTessdataPrefix+"eng" {
		t.Fatalf("tessdata key = %q, want %q", got, DedupKeyTessdataPrefix+"eng")
	}

	hugot, err := HugotSpec("/cfg")
	testutil.AssertNoError(t, err, "hugot spec")
	testutil.AssertEqual(t, hugot.Type, types.Task.Type.Config, "hugot task type")

	payload = configPayload{}
	if err := json.Unmarshal(hugot.Payload, &payload); err != nil {
		t.Fatalf("decode hugot payload: %v", err)
	}
	testutil.AssertEqual(t, payload.Op, OpHugot, "operation")
	testutil.AssertEqual(t, payload.Lang, "", "no language for the model download")
	if got := ConfigDedupKey(hugot.Payload); got != DedupKeyHugot {
		t.Fatalf("hugot key = %q, want %q", got, DedupKeyHugot)
	}
}

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
