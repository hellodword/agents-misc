package state

type TransferObject struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	File   string `json:"file"`
}
