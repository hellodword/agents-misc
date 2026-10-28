package control

import (
	"context"
	"strings"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

// Both the previous and intended value survive a crash between the journal
// write and VirtualBox's write. Only this one documented global key is exposed.
type GUIOwnership struct {
	Original string  `json:"original"`
	Expected string  `json:"expected"`
	Pending  *string `json:"pending,omitempty"`
	Restored bool    `json:"restored"`
}

func (b *VBoxBackend) GUI(ctx context.Context, value *string) (string, error) {
	const key = "GUI/MaxGuestResolution"
	if value != nil {
		args := []string{"setextradata", "global", key}
		if *value != "" {
			args = append(args, *value)
		}
		if _, err := b.run(ctx, args...); err != nil {
			return "", err
		}
	}
	raw, err := b.run(ctx, "getextradata", "global", key)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(raw))
	if s == "No value set!" {
		return "", nil
	}
	if v, ok := strings.CutPrefix(s, "Value: "); ok {
		return v, nil
	}
	return "", wire.Fail("backend_error", "Cannot read the global GUI resolution policy")
}

// Caller holds the runtime's GUI lock (or the exclusive cleanup gate).
func (s *Server) setGUI(ctx context.Context, t *Task, value string, restore bool) error {
	current, err := s.backend.GUI(ctx, nil)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if t.lab().GUI == nil {
		if restore {
			s.mu.Unlock()
			return nil
		}
		t.lab().GUI = &GUIOwnership{Original: current, Expected: current}
	}
	g := t.lab().GUI
	if g.Restored {
		s.mu.Unlock()
		return nil
	}
	if current != g.Expected && (g.Pending == nil || current != *g.Pending) {
		s.mu.Unlock()
		return wire.Fail("gui_conflict", "Global GUI resolution policy was changed externally; inspect it locally before cleanup")
	}
	if restore {
		value = g.Original
	}
	g.Pending = &value
	err = s.db.Save(context.Background(), t)
	s.mu.Unlock()
	if err != nil {
		return err
	}
	observed, err := s.backend.GUI(ctx, &value)
	if err != nil {
		return err
	}
	if observed != value {
		return wire.Fail("gui_conflict", "Global GUI resolution policy could not be confirmed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g.Expected, g.Pending, g.Restored = value, nil, restore
	return s.db.Save(context.Background(), t)
}

func (s *Server) changeGUI(a GUIRequest) (any, error) {
	if !oneOf(a.MaxGuestResolution, "any", "auto") {
		return nil, wire.Fail("invalid_argument", "max_guest_resolution must be any or auto")
	}
	s.mu.Lock()
	t, r, err := s.running(a.TaskID.TaskID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	if err = readGate(ctx, &r.gate); err != nil {
		return nil, err
	}
	defer r.gate.RUnlock()
	lock := s.nodeLock(r, "gui")
	if err = lockContext(ctx, lock); err != nil {
		return nil, err
	}
	defer lock.Unlock()
	s.mu.Lock()
	_, _, err = s.running(a.TaskID.TaskID)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	err = s.setGUI(ctx, t, a.MaxGuestResolution, false)
	return map[string]any{"max_guest_resolution": a.MaxGuestResolution}, err
}
