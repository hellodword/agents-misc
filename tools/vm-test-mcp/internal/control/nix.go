package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) WorkerCacheURL(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capability("transfer", id, "read", time.Now().Add(time.Duration(s.cfg.Tasks.MaxDuration.Seconds()+3600)*time.Second))
}

type workerReader struct {
	ctx              context.Context
	client           WorkerClient
	method, task, id string
	offset           int64
	data             []byte
	eof              bool
}

func (r *workerReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		a := map[string]any{"task_id": r.task, "offset": r.offset}
		if r.id != "" {
			a["artifact_id"] = r.id
		}
		raw, e := r.client.Call(r.ctx, r.method, a)
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
func (b *VBoxBackend) workerEvidence(ctx context.Context, t *Task, raw json.RawMessage, artifacts bool) (json.RawMessage, error) {
	var envelope struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	// An intent may have been recorded before the worker accepted the job.
	if envelope.State == "" {
		return raw, nil
	}
	log := filepath.Join(b.Config.Service.StateDir, "tasks", t.ID, "task.log")
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
	_, e = io.CopyN(f, &workerReader{ctx: ctx, client: b.Worker, method: "log", task: t.ID, offset: st.Size()}, 64<<20)
	f.Close()
	if e != nil && e != io.EOF {
		return nil, e
	}
	if !artifacts {
		return raw, nil
	}
	var data struct {
		State     string `json:"state"`
		Cleanup   string `json:"cleanup"`
		Artifacts []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			Size int64  `json:"size"`
		} `json:"artifacts"`
	}
	if e = json.Unmarshal(raw, &data); e != nil {
		return nil, e
	}
	collected := []*Artifact{}
	for _, a := range data.Artifacts {
		if !wire.IDPattern.MatchString(a.ID) {
			return nil, fmt.Errorf("invalid worker artifact ID")
		}
		relative := filepath.Join("tasks", t.ID, "artifacts", a.ID)
		r := &workerReader{ctx: ctx, client: b.Worker, method: "file", task: t.ID, id: a.ID}
		if e = b.publish(filepath.Join(b.Config.Service.StateDir, relative), r); e != nil {
			return nil, e
		}
		file, e := os.Open(filepath.Join(b.Config.Service.StateDir, relative))
		if e != nil {
			return nil, e
		}
		n, h, e := hashFile(file, b.Config.Storage.MaxUploadBytes)
		file.Close()
		if e != nil {
			return nil, e
		}
		collected = append(collected, &Artifact{ID: a.ID, Name: a.Name, Size: n, SHA256: h, Path: filepath.ToSlash(relative), Created: time.Now().UTC()})
	}
	return wire.JSON(map[string]any{"cleanup": data.Cleanup, "artifacts": collected}), nil
}
