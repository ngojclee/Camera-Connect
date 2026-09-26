// Package coordinator manages the event bus, sync worker queue, and authoritative app state.
package coordinator

import (
	"context"
	"log"
	"sync"
)

// InternalEventType represents typed events flowing through the Agent.
type InternalEventType int

const (
	EvtCameraStarted InternalEventType = iota
	EvtCameraStopped
	EvtNewBackupDetected
	EvtSyncRequested
	EvtSyncCompleted
	EvtSyncFailed
	EvtLockChanged
	EvtConfigChanged
	EvtNetworkAvailable
	EvtNetworkLost
)

// InternalEvent is an event on the internal bus.
type InternalEvent struct {
	Type    InternalEventType
	Payload any
}

// Handler is a function that processes an event.
type Handler func(evt InternalEvent)

// EventBus dispatches typed events to registered handlers.
type EventBus struct {
	mu       sync.RWMutex
	handlers map[InternalEventType][]Handler
	ch       chan InternalEvent
}

// NewEventBus creates an event bus with a buffered channel.
func NewEventBus(bufSize int) *EventBus {
	return &EventBus{
		handlers: make(map[InternalEventType][]Handler),
		ch:       make(chan InternalEvent, bufSize),
	}
}

// On registers a handler for a given event type.
func (b *EventBus) On(evtType InternalEventType, handler Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[evtType] = append(b.handlers[evtType], handler)
}

// Emit sends an event to the bus (non-blocking if buffer not full).
func (b *EventBus) Emit(evt InternalEvent) {
	select {
	case b.ch <- evt:
	default:
		log.Printf("[WARN] event bus full, dropping event type=%d", evt.Type)
	}
}

// Run processes events until context is cancelled.
func (b *EventBus) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt := <-b.ch:
			b.dispatch(evt)
		}
	}
}

func (b *EventBus) dispatch(evt InternalEvent) {
	b.mu.RLock()
	handlers := b.handlers[evt.Type]
	b.mu.RUnlock()

	for _, h := range handlers {
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("[ERROR] event handler panic for type=%d: %v", evt.Type, r)
				}
			}()
			h(evt)
		}()
	}
}
