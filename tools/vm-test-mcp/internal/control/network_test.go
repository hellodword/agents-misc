package control

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type heartbeatBackend struct {
	fakeBackend
	mode  atomic.Int32
	calls atomic.Int32
}

func (f *heartbeatBackend) Network(ctx context.Context, task *Task, protected []string, recover bool) (string, error) {
	f.calls.Add(1)
	switch f.mode.Load() {
	case 1:
		<-ctx.Done()
		return "", networkStage(ctx.Err(), "router_probe")
	case 2:
		return "", networkStage(wire.Fail("network_degraded", "Router identity changed"), "router_identity")
	}
	return f.fakeBackend.Network(ctx, task, protected, recover)
}

func networkFixture(t *testing.T) (*Server, *Task, *Runtime, *heartbeatBackend) {
	t.Helper()
	f := &heartbeatBackend{}
	s, task, r := fixtureLab(t, f, time.Second)
	task.lab().Network = "pending"
	task.lab().LAN, task.lab().DNS, task.lab().DHCP, task.lab().Internet = s.cfg.VirtualBox.Network.LAN, s.cfg.VirtualBox.Network.DNS, true, true
	for i, n := range task.lab().Nodes {
		n.IPv4, n.MAC = fmt.Sprintf("10.77.0.%d", i+2), fmt.Sprintf("08:00:27:00:00:%02x", i+1)
	}
	if err := s.refreshNetworkContext(context.Background(), task, r, false); err != nil {
		t.Fatal(err)
	}
	return s, task, r, f
}

func TestOneLateHeartbeatPreservesLeaseAndReconcilesWithoutRecovery(t *testing.T) {
	s, task, r, f := networkFixture(t)
	command := &Command{ID: wire.ID(), NodeID: task.lab().Nodes[0].ID, CommandStatus: wire.CommandStatus{State: "running"}}
	task.lab().Commands = append(task.lab().Commands, command)
	task.lab().Endpoints = append(task.lab().Endpoints, &Endpoint{ID: wire.ID(), State: "open", Protocol: "tcp", GuestPort: 8080, RouterPort: 20000})
	// Establish a baseline with the declared entry before simulating a late probe.
	task.lab().Endpoints[0].NodeID = task.lab().Nodes[0].ID
	if err := s.refreshNetworkContext(context.Background(), task, r, false); err != nil {
		t.Fatal(err)
	}
	lastLease := task.lab().NetworkLeaseExpires
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		if conn, err := listener.Accept(); err == nil {
			defer conn.Close()
			_, _ = io.Copy(conn, conn)
		}
	}()
	relay, err := OpenRelay(r.ctx, "tcp", "127.0.0.1", 0, listener.Addr().String(), []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()
	r.relays[task.lab().Endpoints[0].ID] = relay
	conn, err := net.DialTimeout("tcp", relay.address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	assertEcho := func() {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		if _, err := conn.Write([]byte("fixture")); err != nil {
			t.Fatal("existing endpoint connection lost", err)
		}
		var buf [7]byte
		if _, err := io.ReadFull(conn, buf[:]); err != nil || string(buf[:]) != "fixture" {
			t.Fatal("existing endpoint connection did not return its payload", err)
		}
	}
	assertEcho()
	f.mode.Store(1)
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Millisecond)
	s.heartbeatNetwork(ctx, task, r)
	cancel()
	if task.lab().Network != "suspect" || task.lab().Endpoints[0].State != "open" || !task.lab().NetworkLeaseExpires.Equal(lastLease) || command.State != "running" {
		t.Fatal("late observation tore down a valid unchanged lease", task.Public())
	}
	assertEcho()
	a := task.lab().NetworkObservation
	if a.Error.Code != "backend_timeout" || a.Stage != "router_probe" || a.BudgetMS <= 0 || a.ExecutionMS <= 0 || a.Finished.Before(a.Started) {
		t.Fatal("missing bounded failure attribution", a)
	}
	f.mode.Store(0)
	ctx, cancel = context.WithTimeout(r.ctx, time.Second)
	s.heartbeatNetwork(ctx, task, r)
	cancel()
	if task.lab().Network != "ready" || len(task.lab().Commands) != 1 || task.lab().Commands[0].ID != command.ID || task.lab().NetworkObservation.Error != nil {
		t.Fatal("reconciliation replayed work or failed to confirm router", task.Public())
	}
	assertEcho()
	items, _, err := s.db.Events(context.Background(), task.ID, 0, 50)
	if err != nil || len(items) != 2 {
		t.Fatal(items, err)
	}
	var events []struct {
		Code    string `json:"code"`
		Details struct {
			Observation *NetworkAttempt `json:"observation"`
			Lease       time.Time       `json:"lease_expires"`
		} `json:"details"`
	}
	// Event envelopes contain lifecycle fields too; decode without rejecting them.
	if err := json.Unmarshal(wire.JSON(items), &events); err != nil || events[0].Code != "network_observation_late" || events[0].Details.Observation.Error.Code != "backend_timeout" || events[0].Details.Lease.IsZero() || events[1].Code != "network_confirmed" {
		t.Fatal(events, err)
	}
	f.mode.Store(2)
	s.heartbeatNetwork(r.ctx, task, r)
	if task.lab().Network != "degraded" || task.lab().Endpoints[0].State != "suspended" || command.State != "running" {
		t.Fatal("identity failure did not isolate network from command result")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	var buf [1]byte
	if n, err := conn.Read(buf[:]); n != 0 || err == nil {
		t.Fatal("degraded network left its established relay open", n, err)
	}
	<-echoDone
}

func TestHeartbeatLockAndExecutionShareOneDeadline(t *testing.T) {
	s, task, r, f := networkFixture(t)
	lock := s.nodeLock(r, "network")
	lock.Lock()
	released := make(chan struct{})
	go func() {
		defer close(released)
		time.Sleep(50 * time.Millisecond)
		lock.Unlock()
	}()
	f.mode.Store(1)
	ctx, cancel := context.WithTimeout(r.ctx, 80*time.Millisecond)
	defer cancel()
	started := time.Now()
	s.heartbeatNetwork(ctx, task, r)
	<-released
	a := task.lab().NetworkObservation
	if time.Since(started) > 300*time.Millisecond || a.BudgetMS > 80 || a.LockWaitMS < 40 || a.ExecutionMS > 80 || task.lab().Network != "suspect" {
		t.Fatal("lock and execution got independent timeout budgets", a, time.Since(started), task.lab().Network)
	}
}

func TestLeaseExpiryAndIdentityFailureRequireExplicitRecovery(t *testing.T) {
	for _, failure := range []string{"lease", "identity", "changed_protection"} {
		t.Run(failure, func(t *testing.T) {
			s, task, r, f := networkFixture(t)
			switch failure {
			case "lease":
				task.lab().NetworkUpdated = time.Now().Add(-time.Minute)
				task.lab().NetworkLeaseExpires = task.lab().NetworkUpdated.Add(15 * time.Second)
			case "identity":
				f.mode.Store(2)
			case "changed_protection":
				s.cfg.VirtualBox.Network.Protected = append(s.cfg.VirtualBox.Network.Protected, "8.8.8.8/32")
				f.mode.Store(1)
			}
			calls := f.calls.Load()
			ctx, cancel := context.WithTimeout(r.ctx, 30*time.Millisecond)
			s.heartbeatNetwork(ctx, task, r)
			cancel()
			if task.lab().Network != "degraded" || task.lab().NetworkObservation.Error == nil {
				t.Fatal("unconfirmed protection remained usable", task.Public())
			}
			if failure == "lease" && (f.calls.Load() != calls || task.lab().NetworkObservation.Error.Code != "network_lease_expired") {
				t.Fatal("expired lease was automatically renewed", task.lab().NetworkObservation)
			}
			f.mode.Store(0)
			calls = f.calls.Load()
			s.heartbeatNetwork(context.Background(), task, r)
			if task.lab().Network != "degraded" || f.calls.Load() != calls {
				t.Fatal("degraded network recovered without explicit request")
			}
			if err := s.refreshNetworkContext(context.Background(), task, r, true); err != nil || task.lab().Network != "ready" {
				t.Fatal("explicit validated recovery failed", err)
			}
		})
	}
}

func TestStaleLeaseExpiryCannotDegradeNewConfirmation(t *testing.T) {
	s, task, r, _ := networkFixture(t)
	s.degrade(task, r, wire.Fail("network_lease_expired", "An earlier observer saw an expired lease"))
	if task.lab().Network != "ready" {
		t.Fatal("stale expiry suspended a new lease")
	}
}
