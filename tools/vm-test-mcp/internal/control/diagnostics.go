package control

import (
	"context"
	"path"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

func (s *Server) diagnostics(ctx context.Context, t, snapshot *Task, n *Node) error {
	artifact := &Artifact{ID: wire.ID(), Name: n.Name + "-VBox.log", Created: time.Now().UTC()}
	artifact.Path = path.Join("tasks", t.ID, "artifacts", artifact.ID)
	if err := s.root.MkdirAll(path.Dir(artifact.Path), 0700); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.backend.Action(ctx, snapshot, n, NodeAction{Action: "diagnostics", Local: path.Join(s.root.Path, artifact.Path)}); err != nil {
		return err
	}
	f, err := s.root.Regular(artifact.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	artifact.Size, artifact.SHA256, err = hashFile(f, s.cfg.Retention.MaxUploadBytes)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Artifacts = append(t.Artifacts, artifact)
	s.save(t)
	return nil
}
