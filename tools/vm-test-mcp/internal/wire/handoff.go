package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
)

type HandoffFile struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// ManifestDigest binds the complete, independently verified file set. The RPC
// sends a fixed-size digest; file metadata is read through bounded pages.
func ManifestDigest(files []HandoffFile) string {
	files = append([]HandoffFile{}, files...)
	sort.Slice(files, func(i, j int) bool { return files[i].ID < files[j].ID })
	h := sha256.Sum256(JSON(files))
	return hex.EncodeToString(h[:])
}
