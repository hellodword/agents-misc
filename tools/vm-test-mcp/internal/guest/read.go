package guest

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func readStream(name string, offset int64, limit int) wire.StreamChunk {
	c := wire.StreamChunk{State: "pending", Offset: offset, Next: offset}
	bad := func(err error) wire.StreamChunk { c.State = "unavailable"; c.Error = wire.Safe(err); return c }
	st, err := os.Lstat(name)
	if os.IsNotExist(err) {
		if offset > 0 {
			return bad(wire.Fail("output_reset", "Previously observed output disappeared"))
		}
		return c
	}
	if err != nil {
		return bad(err)
	}
	if !st.Mode().IsRegular() {
		return bad(wire.Fail("invalid_path", "Command output is not an ordinary file"))
	}
	f, err := os.Open(name)
	if err != nil {
		return bad(err)
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil {
		return bad(err)
	}
	c.Size = st.Size()
	if offset > c.Size {
		return bad(wire.Fail("output_reset", "Command output shrank below its confirmed offset"))
	}
	data := make([]byte, min(int64(limit), c.Size-offset))
	n, err := f.ReadAt(data, offset)
	if err != nil && err != io.EOF {
		return bad(err)
	}
	data = data[:n]
	c.State, c.Next, c.Data = "available", offset+int64(n), base64.StdEncoding.EncodeToString(data)
	h := sha256.Sum256(data)
	c.SHA256 = hex.EncodeToString(h[:])
	return c
}

// Read returns a consistent execution record and independently bounded output
// prefixes. Stream completeness is established by the final status and offsets,
// not merely by reaching the end of the bytes currently available.
func Read(root, id string, stdoutOffset, stderrOffset int64, limit int) (wire.CommandSnapshot, error) {
	v := wire.CommandSnapshot{Protocol: wire.CommandProtocol, CommandID: id, Checked: time.Now().UTC(), Stdout: wire.StreamChunk{State: "pending", Offset: stdoutOffset, Next: stdoutOffset}, Stderr: wire.StreamChunk{State: "pending", Offset: stderrOffset, Next: stderrOffset}}
	if !wire.IDPattern.MatchString(id) || stdoutOffset < 0 || stderrOffset < 0 || limit < 1 || limit > wire.CommandReadLimit {
		return v, wire.Fail("invalid_argument", "Invalid command read ID, byte offset or limit")
	}
	dir := filepath.Join(root, id)
	var status wire.CommandStatus
	if err := readJSON(filepath.Join(dir, "status.json"), &status); err != nil {
		if os.IsNotExist(err) {
			v.Pending = true
			return v, nil
		}
		return v, err
	}
	if !status.Final && status.Supervisor != nil {
		current, alive, err := processIdentity(status.Supervisor.PID)
		if os.IsNotExist(err) || (err == nil && (!alive || current.Start != status.Supervisor.Start)) {
			cause := wire.Fail("supervisor_lost", "The recorded supervisor process no longer exists")
			if err = Reconcile(root, id, cause); err != nil {
				v.Error = phaseFailure("terminal_write", err)
				status = lostSupervisor(status, cause)
			} else if err = readJSON(filepath.Join(dir, "status.json"), &status); err != nil {
				return v, err
			}
		} else if err != nil {
			v.Error = phaseFailure("supervisor_identity", err)
		}
	}
	v.Status = &status
	v.Stdout = readStream(filepath.Join(dir, "stdout"), stdoutOffset, limit)
	v.Stderr = readStream(filepath.Join(dir, "stderr"), stderrOffset, limit)
	if status.Final {
		for _, stream := range []*wire.StreamChunk{&v.Stdout, &v.Stderr} {
			if stream.State == "pending" {
				stream.State = "unavailable"
				stream.Error = &wire.Error{Code: "output_missing", Message: "Final command output file is missing"}
			}
		}
	}
	return v, nil
}
