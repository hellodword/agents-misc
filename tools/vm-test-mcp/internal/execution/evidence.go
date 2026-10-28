package execution

import "time"

type Artifact struct {
	Source  string    `json:"source,omitempty"`
	Pinned  bool      `json:"pinned"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Path    string    `json:"path"`
}
