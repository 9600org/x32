package x32

import (
	"github.com/9600org/go-osc/osc"
	"time"
)

type ExactDispatcher struct {
	handlers map[string]osc.Handler
}

var _ osc.Dispatcher = &ExactDispatcher{}

func NewExactDispatcher() osc.Dispatcher {
	return &ExactDispatcher{
		handlers: make(map[string]osc.Handler),
	}
}

func (s *ExactDispatcher) AddMsgHandler(addr string, f osc.HandlerFunc) error {
	s.handlers[addr] = f
	return nil
}

func (s *ExactDispatcher) Dispatch(packet osc.Packet) {
	switch packet.(type) {
	default:
		return

	case *osc.Message:
		msg, _ := packet.(*osc.Message)
		handler, ok := s.handlers[msg.Address]
		if !ok {
			return
		}
		handler.HandleMessage(msg)

	case *osc.Bundle:
		bundle, _ := packet.(*osc.Bundle)
		timer := time.NewTimer(bundle.Timetag.ExpiresIn())

		go func() {
			<-timer.C
			for _, message := range bundle.Messages {
				handler, ok := s.handlers[message.Address]
				if !ok {
					continue
				}
				handler.HandleMessage(message)
			}

			// Process all bundles
			for _, b := range bundle.Bundles {
				s.Dispatch(b)
			}
		}()
	}
}
