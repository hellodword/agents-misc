package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"testing"
)

func TestIdentityMatchesTheRunningImage(t *testing.T) {
	file, err := os.Executable()
	if runtime.GOOS == "linux" {
		file, err = "/proc/self/exe", nil
	}
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		t.Fatal(err)
	}
	v := Identity()
	if v.BinaryState != "observed" || v.SHA256 != hex.EncodeToString(h.Sum(nil)) || v.Size != size || v.Version != Version || v.GOOS != runtime.GOOS || v.GOARCH != runtime.GOARCH {
		t.Fatal(v)
	}
	want := "unlinked"
	if SourceID != "" {
		want = "linked"
	}
	if v.SourceState != want || v.SourceID != SourceID {
		t.Fatal(v)
	}
	if next := Identity(); next != v {
		t.Fatal("identity changed without a process restart", next, v)
	}
}
