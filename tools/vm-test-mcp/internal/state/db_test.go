package state

import (
	"testing"
)

func TestDurabilitySettingsAndUnknownSchema(t *testing.T) {
	dir := t.TempDir()
	db, e := Open(dir)
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
	if db, e = Open(dir); e == nil {
		db.Close()
		t.Fatal("unknown schema silently overwritten")
	}
}
