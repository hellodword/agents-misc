package wire

import "time"

const CommandProtocol = 1

// Two base64 streams plus status fit within MaxJSON and the host capture limit.
const CommandReadLimit = 256 << 10
const CommandOutputLimit = 64 << 20
const CommandInputLimit = 64 << 20
const InputPacketLimit = 64 << 10
const InputPacketCount = 4096

type ProcessIdentity struct {
	PID   int    `json:"pid"`
	Start string `json:"start"`
}
type CommandSnapshot struct {
	Protocol  int            `json:"protocol"`
	CommandID string         `json:"command_id"`
	Checked   time.Time      `json:"checked"`
	Pending   bool           `json:"pending"`
	Status    *CommandStatus `json:"status,omitempty"`
	Stdout    StreamChunk    `json:"stdout"`
	Stderr    StreamChunk    `json:"stderr"`
	Error     *Error         `json:"error,omitempty"`
}
type StreamChunk struct {
	State  string `json:"state"`
	Offset int64  `json:"offset"`
	Next   int64  `json:"next_offset"`
	Size   int64  `json:"size"`
	Data   string `json:"data,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Error  *Error `json:"error,omitempty"`
}
