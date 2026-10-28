package nixos

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/execution"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/safefs"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type workerLogReader struct {
	ctx            context.Context
	client         Client
	task, instance string
	offset         int64
	data           []byte
	eof            bool
}

func (r *workerLogReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		a := map[string]any{"task_id": r.task, "instance_id": r.instance, "offset": r.offset}
		raw, e := r.client.Call(r.ctx, "log", a)
		if e != nil {
			return 0, e
		}
		var v struct {
			Data       string `json:"data"`
			NextOffset int64  `json:"next_offset"`
			EOF        bool   `json:"eof"`
		}
		if e = wire.Decode(raw, &v); e != nil {
			return 0, e
		}
		r.data, e = base64.StdEncoding.DecodeString(v.Data)
		if e != nil {
			return 0, e
		}
		if v.NextOffset != r.offset+int64(len(r.data)) || len(r.data) == 0 && !v.EOF {
			return 0, fmt.Errorf("invalid worker read offset")
		}
		r.offset = v.NextOffset
		r.eof = v.EOF
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
func (b *Backend) workerEvidence(ctx context.Context, t *Request, raw json.RawMessage, artifacts bool) (json.RawMessage, error) {
	var envelope struct {
		State           string `json:"state"`
		HandoffComplete bool   `json:"handoff_complete"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	// An intent may have been recorded before the worker accepted the job.
	if envelope.State == "" || envelope.HandoffComplete {
		return raw, nil
	}
	log := filepath.Join(b.StateDir, "tasks", t.ID, "task.log")
	if e := os.MkdirAll(filepath.Dir(log), 0700); e != nil {
		return nil, e
	}
	f, e := os.OpenFile(log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return nil, e
	}
	st, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	_, e = io.CopyN(f, &workerLogReader{ctx: ctx, client: b.Client, task: t.ID, instance: t.InstanceID, offset: st.Size()}, 64<<20)
	syncErr := f.Sync()
	f.Close()
	if syncErr != nil {
		return nil, syncErr
	}
	if e != nil && e != io.EOF {
		return nil, e
	}
	if !artifacts {
		return raw, nil
	}
	var data struct {
		State     string             `json:"state"`
		Cleanup   string             `json:"cleanup"`
		Execution *wire.NixExecution `json:"execution"`
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		return nil, e
	}
	manifest := []wire.HandoffFile{}
	// File inventory has its own paging contract; ordinary status stays compact.
	for cursor := int64(0); ; {
		rawPage, err := b.Client.Call(ctx, "files", map[string]any{"task_id": t.ID, "instance_id": t.InstanceID, "cursor": cursor})
		if err != nil {
			return nil, err
		}
		var page struct {
			Items []struct {
				ID     string `json:"id"`
				Name   string `json:"name"`
				Size   int64  `json:"size"`
				SHA256 string `json:"sha256"`
			} `json:"items"`
			Next int64 `json:"next_cursor"`
			EOF  bool  `json:"eof"`
		}
		if err = json.Unmarshal(rawPage, &page); err != nil {
			return nil, err
		}
		for _, a := range page.Items {
			if !wire.IDPattern.MatchString(a.ID) {
				return nil, fmt.Errorf("invalid worker artifact ID")
			}
			relative := filepath.Join("tasks", t.ID, "artifacts", a.ID)
			root, err := safefs.Private(b.StateDir, false)
			if err != nil {
				return nil, err
			}
			var n int64
			var h string
			existing, openErr := root.Regular(filepath.ToSlash(relative))
			if openErr == nil {
				digest := sha256.New()
				n, e = io.Copy(digest, io.LimitReader(existing, b.MaxEvidenceBytes+1))
				existing.Close()
				h = hex.EncodeToString(digest.Sum(nil))
				// A published object is immutable, even if the worker later changes its
				// claim. Reconciliation reuses verified bytes rather than overwriting them.
			} else if os.IsNotExist(openErr) {
				reader, openErr := b.Client.OpenArtifact(ctx, t.InstanceID, t.ID, a.ID)
				if openErr != nil {
					root.Close()
					return nil, openErr
				}
				n, h, e = root.Write(filepath.ToSlash(relative), reader, b.MaxEvidenceBytes)
				reader.Close()
				if e == nil && (n != a.Size || h != a.SHA256) {
					_ = root.Remove(filepath.ToSlash(relative))
				}
			} else {
				e = openErr
			}
			root.Close()
			if e != nil {
				return nil, e
			}
			if n != a.Size || h != a.SHA256 {
				return nil, wire.Fail("evidence_integrity", "Worker evidence size or hash mismatch")
			}

			artifact := &execution.Artifact{Source: "nix_worker", ExecutionID: t.ID, ExecutionKind: "nixos", ID: a.ID, Name: a.Name, Size: n, SHA256: h, Path: filepath.ToSlash(relative), Created: time.Now().UTC()}
			if err = b.PublishEvidence(ctx, artifact); err != nil {
				return nil, err
			}
			manifest = append(manifest, wire.HandoffFile{ID: a.ID, Size: n, SHA256: h})
		}
		if page.EOF {
			break
		}
		if page.Next <= cursor {
			return nil, fmt.Errorf("invalid worker file cursor")
		}
		cursor = page.Next
	}

	return wire.JSON(map[string]any{"cleanup": data.Cleanup, "execution": data.Execution, "manifest_digest": wire.ManifestDigest(manifest)}), nil
}
