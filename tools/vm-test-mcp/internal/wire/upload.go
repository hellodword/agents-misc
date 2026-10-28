package wire

const MaxManifestBytes = 32 << 20

type UploadManifest struct {
	UploadID   string         `json:"upload_id"`
	Derivation string         `json:"derivation,omitempty"`
	System     string         `json:"system,omitempty"`
	Objects    []UploadObject `json:"objects"`
}
type UploadObject struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}
