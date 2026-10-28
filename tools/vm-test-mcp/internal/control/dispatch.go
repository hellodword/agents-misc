package control

import (
	"reflect"
	"time"

	"github.com/hellodword/agents-misc/vm-test-mcp/internal/wire"
)

type pendingDispatch struct {
	ID   string
	Args []byte
}

func (s *Server) resumeDispatches() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pending := range s.pending {
		op := s.operations[pending.ID]
		var args any
		var err error
		for _, def := range Definitions {
			if def.Name == op.Tool {
				value := reflect.New(reflect.TypeOf(def.Args))
				err = wire.Decode(pending.Args, value.Interface())
				if err == nil {
					args, err = s.resolveArguments(op.Tool, value.Elem().Interface())
				}
				break
			}
		}
		if input, ok := args.(InputRequest); ok && err == nil {
			var data []byte
			data, err = s.db.ReadBody(op.Payload.File, op.Payload.Size, op.Payload.SHA256)
			if err == nil {
				input.Data = string(data)
				args = input
			}
		}
		if err == nil && args != nil {
			s.dispatchOperation(op, args, time.Now())
			continue
		}
		op.State = "failed"
		op.IntentStage = "confirmed"
		op.Intent, op.Payload = BodyRef{}, BodyRef{}
		op.Error = wire.Safe(wire.Fail("target_unavailable", "Prepared operation target is unavailable after recovery"))
		s.persist(op)
	}
	s.pending = nil
}
