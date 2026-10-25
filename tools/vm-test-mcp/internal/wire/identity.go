package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"runtime"
	"sync"
)

// SourceID is linked by the product build from its filtered Nix source identity.
// Local development builds leave it empty rather than claiming a source revision.
var SourceID string

type BuildIdentity struct {
	Version     string `json:"version"`
	SourceID    string `json:"source_id,omitempty"`
	SourceState string `json:"source_state"`
	GOOS        string `json:"os"`
	GOARCH      string `json:"arch"`
	GoVersion   string `json:"go_version"`
	BinaryState string `json:"binary_state"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Error       *Error `json:"error,omitempty"`
}

var buildIdentity = sync.OnceValue(readBuildIdentity)

func Identity() BuildIdentity { return buildIdentity() }

func readBuildIdentity() BuildIdentity {
	v := BuildIdentity{Version: Version, SourceID: SourceID, SourceState: "unlinked", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, GoVersion: runtime.Version(), BinaryState: "unavailable"}
	if SourceID != "" {
		v.SourceState = "linked"
	}
	file, err := os.Executable()
	// The path may have been replaced since this process started. Linux's proc
	// link identifies the running image, including an unlinked old deployment.
	if runtime.GOOS == "linux" {
		file, err = "/proc/self/exe", nil
	}
	if err == nil {
		var f *os.File
		f, err = os.Open(file)
		if err == nil {
			defer f.Close()
			h := sha256.New()
			const maximum = 128 << 20
			v.Size, err = io.Copy(h, io.LimitReader(f, maximum+1))
			if err == nil && v.Size > maximum {
				err = Fail("identity_limit", "Executable exceeds the identity read budget")
			}
			if err == nil {
				v.BinaryState, v.SHA256 = "observed", hex.EncodeToString(h.Sum(nil))
			}
		}
	}
	if err != nil {
		v.Error = &Error{Code: "build_identity_unavailable", Message: "Could not fingerprint the running executable"}
	}
	return v
}
