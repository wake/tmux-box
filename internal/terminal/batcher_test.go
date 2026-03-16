// internal/terminal/batcher_test.go
package terminal_test

import (
	"testing"
	"time"

	"github.com/wake/tmux-box/internal/terminal"
)

func TestBatcherFlushByTime(t *testing.T) {
	ch := make(chan []byte, 10)
	b := terminal.NewBatcher(20*time.Millisecond, 64*1024, func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		ch <- cp
	})
	defer b.Stop()

	b.Write([]byte("hello"))

	select {
	case got := <-ch:
		if string(got) != "hello" {
			t.Errorf("want hello, got %s", got)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for flush")
	}
}

func TestBatcherFlushBySize(t *testing.T) {
	ch := make(chan []byte, 10)
	b := terminal.NewBatcher(1*time.Second, 10, func(data []byte) {
		cp := make([]byte, len(data))
		copy(cp, data)
		ch <- cp
	})
	defer b.Stop()

	b.Write([]byte("12345678901")) // 11 bytes > 10 threshold

	select {
	case got := <-ch:
		if len(got) != 11 {
			t.Errorf("want 11 bytes, got %d", len(got))
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for size flush")
	}
}
