// internal/bridge/bridge.go
package bridge

import "sync"

// SubID is an opaque subscriber identifier used for unsubscription.
// ID-based tracking is used instead of channel comparison because
// Go's chan T and <-chan T are different types that can't be compared with ==.
type SubID uint64

// Bridge manages pub-sub between tbox relay (producer) and SPA clients (consumers).
// Each session has at most one relay and zero or more subscribers.
type Bridge struct {
	mu       sync.RWMutex
	sessions map[string]*sessionBridge
}

type sessionBridge struct {
	relayCh     chan []byte           // SPA → relay direction
	subscribers map[uint64]chan []byte // id → channel, relay → SPA direction
	nextID      uint64
}

// New creates a new Bridge.
func New() *Bridge {
	return &Bridge{sessions: make(map[string]*sessionBridge)}
}

// RegisterRelay registers a relay producer for the named session.
// Returns a channel from which the relay reads SPA user input.
func (b *Bridge) RegisterRelay(name string) <-chan []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	sb := &sessionBridge{
		relayCh:     make(chan []byte, 64),
		subscribers: make(map[uint64]chan []byte),
	}
	b.sessions[name] = sb
	return sb.relayCh
}

// UnregisterRelay removes a relay and closes all subscriber channels.
func (b *Bridge) UnregisterRelay(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if sb, ok := b.sessions[name]; ok {
		close(sb.relayCh)
		for _, ch := range sb.subscribers {
			close(ch)
		}
		delete(b.sessions, name)
	}
}

// HasRelay returns true if a relay is registered for the named session.
func (b *Bridge) HasRelay(name string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.sessions[name]
	return ok
}

// Subscribe adds a consumer for the named session.
// Returns (0, nil) if no relay is registered for the session.
func (b *Bridge) Subscribe(name string) (SubID, <-chan []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sb, ok := b.sessions[name]
	if !ok {
		return 0, nil
	}
	sb.nextID++
	ch := make(chan []byte, 64)
	sb.subscribers[sb.nextID] = ch
	return SubID(sb.nextID), ch
}

// Unsubscribe removes a consumer and closes its channel.
func (b *Bridge) Unsubscribe(name string, id SubID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sb, ok := b.sessions[name]
	if !ok {
		return
	}
	if ch, exists := sb.subscribers[uint64(id)]; exists {
		close(ch)
		delete(sb.subscribers, uint64(id))
	}
}

// RelayToSubscribers fans out data from the relay to all subscribers.
// Slow subscribers are dropped (non-blocking send).
func (b *Bridge) RelayToSubscribers(name string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[name]
	if !ok {
		return
	}
	for _, ch := range sb.subscribers {
		select {
		case ch <- data:
		default: // drop if slow
		}
	}
}

// SubscriberToRelay sends data from a subscriber to the relay.
// Dropped if the relay channel is full (non-blocking send).
func (b *Bridge) SubscriberToRelay(name string, data []byte) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sb, ok := b.sessions[name]
	if !ok {
		return
	}
	select {
	case sb.relayCh <- data:
	default:
	}
}
