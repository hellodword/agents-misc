package guest

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
)

type outputStreams struct {
	readers []*os.File
	done    chan struct{}
	mu      sync.Mutex
	err     error
}

func drainStreams(stdout, stderr *os.File, out, errout io.Writer) *outputStreams {
	s := &outputStreams{readers: []*os.File{stdout, stderr}, done: make(chan struct{})}
	var wg sync.WaitGroup
	for i, w := range []io.Writer{out, errout} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.readers[i].Close()
			_, err := io.Copy(w, s.readers[i])
			s.mu.Lock()
			s.err = errors.Join(s.err, err)
			s.mu.Unlock()
		}()
	}
	go func() { wg.Wait(); close(s.done) }()
	return s
}
func (s *outputStreams) Wait(ctx context.Context) error {
	select {
	case <-s.done:
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.err
	case <-ctx.Done():
		s.Close()
		return ctx.Err()
	}
}
func (s *outputStreams) Close() {
	if s == nil {
		return
	}
	for _, r := range s.readers {
		_ = r.Close()
	}
}
