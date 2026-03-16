// internal/terminal/batcher.go
package terminal

import (
	"sync"
	"time"
)

type Batcher struct {
	interval time.Duration
	maxSize  int
	onFlush  func([]byte)
	buf      []byte
	mu       sync.Mutex
	timer    *time.Timer
	stopped  bool
}

func NewBatcher(interval time.Duration, maxSize int, onFlush func([]byte)) *Batcher {
	return &Batcher{interval: interval, maxSize: maxSize, onFlush: onFlush}
}

func (b *Batcher) Write(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return
	}
	b.buf = append(b.buf, data...)
	if len(b.buf) >= b.maxSize {
		b.flushLocked()
		return
	}
	if b.timer == nil {
		b.timer = time.AfterFunc(b.interval, func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			b.flushLocked()
		})
	}
}

func (b *Batcher) flushLocked() {
	if len(b.buf) == 0 {
		return
	}
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	b.buf = b.buf[:0]
	if b.timer != nil {
		b.timer.Stop()
		b.timer = nil
	}
	b.mu.Unlock()
	b.onFlush(out)
	b.mu.Lock()
}

func (b *Batcher) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	if b.timer != nil {
		b.timer.Stop()
	}
	b.flushLocked()
}
