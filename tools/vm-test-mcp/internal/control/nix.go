package control

import "time"

func (s *Server) WorkerCacheURL(id string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.capability("transfer", id, "read", time.Now().Add(time.Duration(s.cfg.Tasks.MaxDuration.Seconds()+3600)*time.Second))
}
