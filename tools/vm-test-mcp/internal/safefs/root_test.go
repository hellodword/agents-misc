package safefs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfinedFilesAndLock(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	r, e := Private(dir, true)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if other, e := Private(dir, true); e == nil {
		other.Close()
		t.Fatal("second owner acquired state")
	}
	if _, _, e = r.Write("nested/ok", strings.NewReader("safe"), 10); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"../outside", "/etc/passwd", "x/../../secret", "a\\b"} {
		if _, _, e = r.Write(name, strings.NewReader("bad"), 10); e == nil {
			t.Fatal(name)
		}
	}
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "data"), []byte("secret"), 0600)
	os.Symlink(outside, filepath.Join(dir, "link"))
	if _, e = r.Regular("link/data"); e == nil {
		t.Fatal("followed symlink")
	}
	if _, _, e = r.Write("link/data", strings.NewReader("bad"), 10); e == nil {
		t.Fatal("escaped root")
	}
	data, _ := os.ReadFile(filepath.Join(outside, "data"))
	if string(data) != "secret" {
		t.Fatal("modified outside data")
	}
	if _, _, e = r.Write("nested/ok", strings.NewReader("too large"), 3); e == nil {
		t.Fatal("quota ignored")
	}
	data, e = r.Read("nested/ok", 10)
	if e != nil || string(data) != "safe" {
		t.Fatal("failed write replaced original", e)
	}
}
