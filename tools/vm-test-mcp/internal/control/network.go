package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/network"
	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

const networkBudget = 5 * time.Second

type networkProgressKey struct{}
type networkProgress func(string)

func reportNetworkProgress(ctx context.Context, stage string) {
	if f, ok := ctx.Value(networkProgressKey{}).(networkProgress); ok {
		f(stage)
	}
}

type NetworkAttempt struct {
	clockStart  time.Time
	Stage       string      `json:"stage"`
	Started     time.Time   `json:"started"`
	Deadline    time.Time   `json:"deadline"`
	Finished    time.Time   `json:"finished"`
	BudgetMS    int64       `json:"budget_ms"`
	LockWaitMS  int64       `json:"lock_wait_ms"`
	ExecutionMS int64       `json:"execution_ms"`
	Error       *wire.Error `json:"error,omitempty"`
	PolicyHash  string      `json:"policy_hash,omitempty"`
}

func routerIdentity(t *Task) any {
	var id string
	if t.Router != nil {
		id = t.Router.ID
	}
	fingerprint := ""
	if t.RouterBootID != "" {
		h := sha256.Sum256([]byte(t.RouterBootID))
		fingerprint = hex.EncodeToString(h[:])
	}
	return map[string]any{"node_id": id, "boot_fingerprint": fingerprint}
}

func (s *Server) protections() ([]string, error) {
	extra := append([]string{}, s.cfg.VirtualBox.Network.Protected...)
	for _, a := range s.cfg.VirtualBox.Network.PublicHostAddresses {
		extra = append(extra, a+"/32")
	}
	return network.HostProtection(extra)
}

func attempt(ctx context.Context, stage string) *NetworkAttempt {
	now, end := time.Now(), deadline(ctx)
	return &NetworkAttempt{clockStart: now, Stage: stage, Started: now.UTC(), Deadline: end.UTC(), BudgetMS: max(0, end.Sub(now).Milliseconds())}
}

func (s *Server) recordNetworkAttempt(t *Task, a *NetworkAttempt, err error) {
	a.Finished = time.Now().UTC()
	a.ExecutionMS = max(0, time.Since(a.clockStart).Milliseconds()-a.LockWaitMS)
	if err != nil {
		a.Error = wire.Safe(err)
		if stage, ok := a.Error.Details["stage"].(string); ok {
			a.Stage = stage
		}
	}
	s.mu.Lock()
	t.NetworkObservation = a
	s.save(t)
	s.mu.Unlock()
}

// A heartbeat uses one deadline for lock waiting, Guest Control and policy
// application. A late observation is tolerable only while an unchanged, confirmed
// policy lease still protects the network. Explicit mutations fail closed.
func (s *Server) heartbeatNetwork(parent context.Context, t *Task, r *Runtime) {
	ctx, cancel := context.WithTimeout(parent, networkBudget)
	defer cancel()
	a := attempt(ctx, "network_lock")
	lock := s.nodeLock(r, "network")
	err := lockContext(ctx, lock)
	a.LockWaitMS = time.Since(a.clockStart).Milliseconds()
	if err == nil {
		defer lock.Unlock()
		s.mu.Lock()
		active := oneOf(t.Network, "ready", "suspect") && !t.Terminal()
		s.mu.Unlock()
		if !active {
			return
		}
		err = s.refreshNetworkAttempt(ctx, t, r, false, a)
	} else {
		s.recordNetworkAttempt(t, a, err)
	}
	if err == nil || r.ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	transient := oneOf(wire.Safe(err).Code, "backend_timeout", "resource_busy")
	unchanged := a.PolicyHash == "" && a.Stage == "network_lock" || a.PolicyHash != "" && a.PolicyHash == t.NetworkPolicyHash
	tolerate := transient && unchanged && !t.Terminal() && oneOf(t.Network, "ready", "suspect") && time.Now().Before(t.NetworkLeaseExpires)
	if tolerate {
		if t.Network != "suspect" {
			t.Network = "suspect"
			s.eventDetails(t, "network_observation_late", "Network observation is late within the confirmed policy lease", map[string]any{"observation": a, "lease_expires": t.NetworkLeaseExpires, "router_identity": routerIdentity(t)})
		}
		s.save(t)
	}
	s.mu.Unlock()
	if !tolerate {
		s.degrade(t, r, err)
	}
}

func networkStage(err error, stage string) error {
	if err == nil {
		return nil
	}
	cause := wire.Safe(err)
	return &wire.Error{Code: cause.Code, Message: cause.Message, Details: map[string]any{"stage": stage, "cause": cause}}
}

func (s *Server) refreshNetworkContext(parent context.Context, t *Task, r *Runtime, recover bool) error {
	budget := networkBudget
	if recover {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	return s.refreshNetworkAttempt(ctx, t, r, recover, attempt(ctx, "host_protection"))
}

func (s *Server) refreshNetworkAttempt(ctx context.Context, t *Task, r *Runtime, recover bool, a *NetworkAttempt) (err error) {
	defer func() { s.recordNetworkAttempt(t, a, err) }()
	a.Stage = "host_protection"
	protected, err := s.protections()
	if err != nil {
		return err
	}
	s.mu.Lock()
	copyTask := t.executionSnapshot()
	s.mu.Unlock()
	policy := taskPolicy(copyTask, s.cfg.VirtualBox.Network.WAN, s.cfg.VirtualBox.Network.LeaseSeconds, protected)
	h := sha256.Sum256(wire.JSON(policy))
	a.PolicyHash = hex.EncodeToString(h[:])
	a.Stage = "lease"
	if !recover && (copyTask.Network == "degraded" || oneOf(copyTask.Network, "ready", "suspect") && (copyTask.NetworkUpdated.IsZero() || time.Since(copyTask.NetworkUpdated) >= time.Duration(s.cfg.VirtualBox.Network.LeaseSeconds)*time.Second)) {
		return wire.Fail("network_lease_expired", "Network lease is unavailable; explicit recovery is required")
	}
	a.Stage = "router_policy"
	// Use the request's start, not its delayed reply, as the conservative lease
	// origin. It cannot overstate the guest's actual remaining lease lifetime.
	leaseStart := time.Now().UTC()
	progress := networkProgress(func(stage string) {
		a.Stage = stage
		if stage == "policy_apply" {
			leaseStart = time.Now().UTC()
		}
	})
	bootID, err := s.backend.Network(context.WithValue(ctx, networkProgressKey{}, progress), copyTask, protected, recover)
	if err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	leaseEnd := leaseStart.Add(time.Duration(s.cfg.VirtualBox.Network.LeaseSeconds) * time.Second)
	if !time.Now().Before(leaseEnd) {
		return wire.Fail("network_lease_expired", "Policy acknowledgement arrived after its conservative lease deadline")
	}
	if recover {
		a.Stage = "endpoint_restore"
		for _, entry := range copyTask.Endpoints {
			if entry.State == "closed" {
				continue
			}
			if err = s.openRelay(ctx, t, r, entry); err != nil {
				s.closeRelays(t, r)
				return err
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t.Terminal() || t.Network == "degraded" && !recover {
		return wire.Fail("network_degraded", "Task or network state changed before policy acknowledgement")
	}
	late := t.Network == "suspect"
	t.Network, t.RouterBootID, t.NetworkUpdated = "ready", bootID, leaseStart
	t.NetworkLeaseExpires, t.NetworkPolicyHash = leaseEnd, a.PolicyHash
	if recover {
		for _, e := range t.Endpoints {
			if e.State != "closed" {
				e.State = "open"
			}
		}
	}
	a.Stage = "confirmed"
	if late || recover {
		s.eventDetails(t, "network_confirmed", "Router identity and policy lease confirmed", map[string]any{"explicit_recovery": recover, "lease_expires": leaseEnd, "router_identity": routerIdentity(t)})
	}
	s.save(t)
	return nil
}
