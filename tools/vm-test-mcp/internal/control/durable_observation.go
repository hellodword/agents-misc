package control

import "time"

var zeroTime time.Time

// Health sample times and counters serve live hysteresis. Durable records keep
// capability transitions and fault evidence, independently of sampling cadence.
func durableObservation(v Observation) Observation {
	return Observation{State: v.State, FailureSince: v.FailureSince, Error: v.Error}
}
func durableNode(n *Node) *Node {
	v := *n
	v.Polling = PollSchedule{}
	v.Health = Health{VirtualBox: durableObservation(n.Health.VirtualBox), GuestControl: durableObservation(n.Health.GuestControl), Commands: durableObservation(n.Health.Commands)}
	v.Preparation.Updated = zeroTime
	if v.Guest != nil {
		v.Guest = *clone(&n.Guest)
		if network, ok := v.Guest["network"].(map[string]any); ok {
			delete(network, "checked")
		}
		if service, ok := v.Guest["command_service"].(map[string]any); ok {
			if heartbeat, ok := service["heartbeat"].(map[string]any); ok {
				delete(heartbeat, "time")
			}
		}
	}
	if n.GuestSessions != nil {
		sessions := *n.GuestSessions
		sessions.Checked = zeroTime
		sessions.Observed = nil
		if sessions.Error == nil && sessions.State == "ready" {
			sessions.Before = nil
			sessions.After = nil
			sessions.Matched = 0
			sessions.InFlight = 0
			sessions.CloseRequested = 0
			sessions.LiveOwner = 0
		}
		v.GuestSessions = &sessions
	}
	return &v
}
