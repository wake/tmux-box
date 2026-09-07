package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPidFileLockAndUnlock(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")

	// Acquire lock
	f, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("acquirePidLock: %v", err)
	}

	// PID file should contain our PID
	data, _ := os.ReadFile(pidPath)
	pid, _ := strconv.Atoi(string(data))
	if pid != os.Getpid() {
		t.Errorf("pid file = %d, want %d", pid, os.Getpid())
	}

	// Second acquire should fail
	_, err = acquirePidLock(pidPath, os.Getpid()+1)
	if err == nil {
		t.Fatal("expected error for second lock, got nil")
	}

	// Release
	releasePidLock(f, pidPath)

	// After release, acquire should succeed again
	f2, err := acquirePidLock(pidPath, os.Getpid())
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	releasePidLock(f2, pidPath)
}

func TestIsDaemonRunning(t *testing.T) {
	dir := t.TempDir()
	pidPath := filepath.Join(dir, "pdx.pid")

	// No PID file — not running
	running, pid := isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running when no PID file")
	}
	if pid != 0 {
		t.Errorf("expected pid=0, got %d", pid)
	}

	// Lock held — running
	f, _ := acquirePidLock(pidPath, os.Getpid())
	running, pid = isDaemonRunning(pidPath)
	if !running {
		t.Error("expected running when lock held")
	}
	if pid != os.Getpid() {
		t.Errorf("expected pid=%d, got %d", os.Getpid(), pid)
	}
	releasePidLock(f, pidPath)

	// Stale PID file (no lock held) — not running
	os.WriteFile(pidPath, []byte("99999"), 0644)
	running, _ = isDaemonRunning(pidPath)
	if running {
		t.Error("expected not running with stale PID file")
	}
}

func TestWaitForHealthyImmediate(t *testing.T) {
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&probes, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	notified := 0
	start := time.Now()
	err := waitForHealthy(srv.URL, make(chan error, 1), 500*time.Millisecond, 10*time.Millisecond, func(string) {
		notified++
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForHealthy: %v", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("returned after %v, expected to return well under the timeout", elapsed)
	}
	if got := atomic.LoadInt32(&probes); got != 1 {
		t.Errorf("probes = %d, want 1", got)
	}
	if notified != 0 {
		t.Errorf("notify called %d times on the fast path, want 0", notified)
	}
}

func TestWaitForHealthyAfterSeveralProbes(t *testing.T) {
	const failures = 3
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&probes, 1) <= failures {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := waitForHealthy(srv.URL, make(chan error, 1), 2*time.Second, 5*time.Millisecond, nil)
	if err != nil {
		t.Fatalf("waitForHealthy: %v", err)
	}
	if got := atomic.LoadInt32(&probes); got != failures+1 {
		t.Errorf("probes = %d, want %d", got, failures+1)
	}
}

func TestWaitForHealthyTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	timeout := 100 * time.Millisecond
	start := time.Now()
	err := waitForHealthy(srv.URL, make(chan error, 1), timeout, 10*time.Millisecond, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the server never becomes healthy")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("error = %q, want it to mention the elapsed window", err)
	}
	if elapsed < timeout {
		t.Errorf("returned after %v, expected it to use the whole %v window", elapsed, timeout)
	}
	if elapsed > timeout+400*time.Millisecond {
		t.Errorf("returned after %v, far beyond the %v window", elapsed, timeout)
	}
}

func TestWaitForHealthyChildExitsEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	timeout := 1 * time.Second
	childExited := make(chan error, 1)
	go func() {
		time.Sleep(15 * time.Millisecond)
		childExited <- errors.New("exit status 1")
	}()

	start := time.Now()
	err := waitForHealthy(srv.URL, childExited, timeout, 5*time.Millisecond, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error when the child exits during startup")
	}
	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("error = %q, want it to report the child exit", err)
	}
	if strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("error = %q, want the child-exit wording, not the timeout wording", err)
	}
	if !strings.Contains(err.Error(), "exit status 1") {
		t.Errorf("error = %q, want it to name the child's exit status", err)
	}
	if elapsed > 250*time.Millisecond {
		t.Errorf("returned after %v, expected to return promptly, well under the %v timeout", elapsed, timeout)
	}
}

func TestWaitForHealthyNotifiesOnSlowWait(t *testing.T) {
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&probes, 1) < 12 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var mu sync.Mutex
	var msgs []string
	notify := func(m string) {
		mu.Lock()
		defer mu.Unlock()
		msgs = append(msgs, m)
	}

	err := waitForHealthyWithNotify(srv.URL, make(chan error, 1), 2*time.Second, 5*time.Millisecond, 20*time.Millisecond, notify)
	if err != nil {
		t.Fatalf("waitForHealthy: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != 1 {
		t.Fatalf("notify called %d times, want exactly 1 (msgs: %v)", len(msgs), msgs)
	}
	if msgs[0] == "" {
		t.Error("notify message is empty")
	}
}
