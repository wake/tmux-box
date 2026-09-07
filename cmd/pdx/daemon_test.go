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
	err := waitForHealthy(srv.URL, make(chan error, 1), 2*time.Second, 40*time.Millisecond, func(string) {
		notified++
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("waitForHealthy: %v", err)
	}
	// A single successful probe must return immediately, nowhere near the window.
	if elapsed > 500*time.Millisecond {
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

	err := waitForHealthy(srv.URL, make(chan error, 1), 4*time.Second, 20*time.Millisecond, nil)
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

	timeout := 150 * time.Millisecond
	start := time.Now()
	err := waitForHealthy(srv.URL, make(chan error, 1), timeout, 40*time.Millisecond, nil)
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
	if elapsed > timeout+600*time.Millisecond {
		t.Errorf("returned after %v, far beyond the %v window", elapsed, timeout)
	}
}

func TestWaitForHealthyChildExitsEarly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	timeout := 4 * time.Second
	childExited := make(chan error, 1)
	go func() {
		time.Sleep(40 * time.Millisecond)
		childExited <- errors.New("exit status 1")
	}()

	start := time.Now()
	err := waitForHealthy(srv.URL, childExited, timeout, 20*time.Millisecond, nil)
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
	// Still an order of magnitude below the window: the point is that the wait
	// ends on the child's death, not that it ends at a precise instant.
	if elapsed > 1*time.Second {
		t.Errorf("returned after %v, expected to return promptly, well under the %v timeout", elapsed, timeout)
	}
}

func TestWaitForHealthyNotifiesOnSlowWait(t *testing.T) {
	var probes int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&probes, 1) < 8 {
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

	err := waitForHealthyWithNotify(srv.URL, make(chan error, 1), 4*time.Second, 20*time.Millisecond, 60*time.Millisecond, notify)
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

// A daemon can answer 200 and die immediately afterwards: cmd.Wait() delivers the
// exit status while the parent is still holding the HTTP response. Accepting that
// 200 makes `pdx start` report success for a process that is already gone.
func TestWaitForHealthyChildExitsDuringProbe(t *testing.T) {
	childExited := make(chan error, 1)
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Publishing the exit before the header is written makes the ordering
		// deterministic: the client cannot observe the 200 until after the send.
		once.Do(func() { childExited <- errors.New("exit status 2") })
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := waitForHealthy(srv.URL, childExited, 2*time.Second, 20*time.Millisecond, nil)
	if err == nil {
		t.Fatal("accepted a 200 from a daemon that had already exited during startup")
	}
	if !strings.Contains(err.Error(), "exited during startup") {
		t.Errorf("error = %q, want it to report the child exit", err)
	}
	if !strings.Contains(err.Error(), "exit status 2") {
		t.Errorf("error = %q, want it to name the child's exit status", err)
	}
}

// A probe started near the end of the window must not be allowed to run past it:
// a 200 that only arrives after the deadline is not evidence of a healthy start,
// and waiting for it overruns the advertised window.
func TestWaitForHealthyRejectsResponseAfterDeadline(t *testing.T) {
	const (
		timeout   = 250 * time.Millisecond
		interval  = 80 * time.Millisecond
		stallFrom = 120 * time.Millisecond
		stallFor  = 230 * time.Millisecond
	)

	start := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if time.Since(start) < stallFrom {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		// Any probe from here on answers 200, but only after the window has closed.
		select {
		case <-r.Context().Done():
			return
		case <-time.After(stallFor):
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := waitForHealthy(srv.URL, make(chan error, 1), timeout, interval, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("accepted a 200 that arrived after the health window had closed")
	}
	if !strings.Contains(err.Error(), "did not become healthy") {
		t.Errorf("error = %q, want the timeout wording", err)
	}
	if elapsed < timeout {
		t.Errorf("returned after %v, expected it to use the whole %v window", elapsed, timeout)
	}
	if elapsed > timeout+250*time.Millisecond {
		t.Errorf("returned after %v, overran the %v window", elapsed, timeout)
	}
}
