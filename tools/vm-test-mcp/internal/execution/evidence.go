package execution

import "time"

type Artifact struct {
	ExecutionID   string `json:"execution_id"`
	ExecutionKind string `json:"execution_kind"`

	Source  string    `json:"source,omitempty"`
	Pinned  bool      `json:"pinned"`
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	SHA256  string    `json:"sha256"`
	Created time.Time `json:"created"`
	Path    string    `json:"path"`
}
