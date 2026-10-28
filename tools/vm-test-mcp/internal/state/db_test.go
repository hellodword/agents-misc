package state

import (
	"testing"
)

func TestDurabilitySettingsAndUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	db, e := OpenController(dir)
	if e != nil {
		t.Fatal(e)
	}
	for pragma, want := range map[string]string{"foreign_keys": "1", "journal_mode": "wal", "synchronous": "2", "busy_timeout": "5000"} {
		var got string
		if e = db.QueryRow("PRAGMA " + pragma).Scan(&got); e != nil || got != want {
			t.Fatalf("%s=%s: %v", pragma, got, e)
		}
	}
	if _, e = db.Exec("PRAGMA user_version=99"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if db, e = OpenController(dir); e == nil {
		db.Close()
		t.Fatal("unknown schema silently overwritten")
	}
}

func TestRoleSchemasAndInstanceIdentity(t *testing.T) {
	for _, role := range []string{"controller", "nix-worker"} {
		dir := t.TempDir()
		openRole := OpenController
		own, absent := "executions", "worker_jobs"
		if role == "nix-worker" {
			openRole, own, absent = OpenWorker, "worker_jobs", "executions"
		}
		db, err := openRole(dir)
		if err != nil {
			t.Fatal(err)
		}
		identity := db.InstanceID
		var count int
		if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", own).Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
		if err = db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", absent).Scan(&count); err != nil || count != 0 {
			t.Fatal(count, err)
		}
		db.Close()
		db, err = openRole(dir)
		if err != nil {
			t.Fatal(err)
		}
		if identity == "" || db.InstanceID != identity {
			t.Fatal("storage identity changed on reopen")
		}
		db.Close()
		other := OpenWorker
		if role == "nix-worker" {
			other = OpenController
		}
		if db, err = other(dir); err == nil {
			db.Close()
			t.Fatal("opened other role's database")
		}
	}
}
