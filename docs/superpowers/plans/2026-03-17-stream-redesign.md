# Stream Mode Redesign Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace daemon-spawned claude subprocess with `tbox relay` running inside tmux sessions, bridging back via WebSocket. Redesign stream UI to Claude Code style.

**Architecture:** `tbox relay` wraps user's claude command inside tmux, bridges stdin/stdout to daemon via WebSocket (`/ws/cli-bridge/`). Daemon transparently relays NDJSON between `tbox relay` and SPA clients. Session status detected via tmux polling. UI removes avatars, adds thinking indicator, card-style input.

**Tech Stack:** Go 1.25+ / net/http / gorilla/websocket / BurntSushi/toml / creack/pty / React 19 / Zustand / Tailwind CSS / Phosphor Icons / vitest

**Spec:** `docs/superpowers/specs/2026-03-17-stream-redesign.md`

---

## Phase A: Go Daemon Foundation

### Task 1: Extend Config for Presets and Detect

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write failing test for new config fields**

```go
// config_test.go — add test case
func TestLoadConfigWithPresets(t *testing.T) {
    dir := t.TempDir()
    path := filepath.Join(dir, "config.toml")
    os.WriteFile(path, []byte(`
bind = "0.0.0.0"
port = 8080

[[stream.presets]]
name = "cc"
command = "claude -p --input-format stream-json --output-format stream-json"

[[stream.presets]]
name = "dangerous"
command = "claude -p --input-format stream-json --output-format stream-json --dangerously-skip-permissions"

[[jsonl.presets]]
name = "cc"
command = ""

[detect]
cc_commands = ["claude", "cld"]
poll_interval = 3
`), 0644)

    cfg, err := config.Load(path)
    if err != nil {
        t.Fatal(err)
    }
    if len(cfg.Stream.Presets) != 2 {
        t.Fatalf("expected 2 stream presets, got %d", len(cfg.Stream.Presets))
    }
    if cfg.Stream.Presets[0].Name != "cc" {
        t.Fatalf("expected first preset name 'cc', got %q", cfg.Stream.Presets[0].Name)
    }
    if len(cfg.Detect.CCCommands) != 2 {
        t.Fatalf("expected 2 cc_commands, got %d", len(cfg.Detect.CCCommands))
    }
    if cfg.Detect.PollInterval != 3 {
        t.Fatalf("expected poll_interval 3, got %d", cfg.Detect.PollInterval)
    }
}

func TestLoadConfigDefaults(t *testing.T) {
    cfg, _ := config.Load("/nonexistent")
    if len(cfg.Stream.Presets) != 1 {
        t.Fatalf("expected 1 default stream preset, got %d", len(cfg.Stream.Presets))
    }
    if cfg.Detect.PollInterval != 2 {
        t.Fatalf("expected default poll_interval 2, got %d", cfg.Detect.PollInterval)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/config/ -run TestLoadConfigWith -v`
Expected: FAIL — `cfg.Stream` undefined

- [ ] **Step 3: Implement config changes**

Add to `internal/config/config.go`:

```go
type Preset struct {
    Name    string `toml:"name"    json:"name"`
    Command string `toml:"command" json:"command"`
}

type StreamConfig struct {
    Presets []Preset `toml:"presets" json:"presets"`
}

type JSONLConfig struct {
    Presets []Preset `toml:"presets" json:"presets"`
}

type DetectConfig struct {
    CCCommands   []string `toml:"cc_commands"   json:"cc_commands"`
    PollInterval int      `toml:"poll_interval" json:"poll_interval"`
}

// Add fields to Config struct:
type Config struct {
    Bind         string       `toml:"bind"`
    Port         int          `toml:"port"`
    Token        string       `toml:"token"`
    Allow        []string     `toml:"allow"`
    DataDir      string       `toml:"data_dir"`
    AllowedPaths []string     `toml:"allowed_paths"`
    Stream       StreamConfig `toml:"stream"`
    JSONL        JSONLConfig  `toml:"jsonl"`
    Detect       DetectConfig `toml:"detect"`
}
```

Update `Load()` defaults to include:
```go
cfg := Config{
    Bind:    "127.0.0.1",
    Port:    7860,
    DataDir: defaultDataDir(),
    Stream: StreamConfig{
        Presets: []Preset{{
            Name:    "cc",
            Command: "claude -p --input-format stream-json --output-format stream-json",
        }},
    },
    Detect: DetectConfig{
        CCCommands:   []string{"claude"},
        PollInterval: 2,
    },
}
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add stream/jsonl presets and detect settings"
```

---

### Task 2: Delete Old Stream Subsystem

**Files:**
- Delete: `internal/stream/session.go`
- Delete: `internal/stream/session_test.go`
- Delete: `internal/stream/manager.go`
- Delete: `internal/stream/manager_test.go`
- Delete: `internal/server/stream_handler.go`
- Delete: `internal/server/stream_handler_test.go`
- Modify: `internal/server/server.go` — remove stream routes + Manager dep
- Modify: `internal/server/session_handler.go` — remove stream Manager usage
- Modify: `internal/server/session_handler_test.go` — update tests
- Modify: `cmd/tbox/main.go` — remove stream.Manager init

**Note:** Steps 1-4 must ALL be completed before running tests (Step 6). They form one atomic change — the code won't compile in intermediate states.

- [ ] **Step 1: Remove stream package references from server.go**

In `internal/server/server.go`:
- Remove `stream` import
- Remove `streams *stream.Manager` field from Server struct
- Remove `NewServer` parameter for streams
- Remove `/ws/stream/{session}` route
- Remove `handleStream` method

- [ ] **Step 2: Remove stream Manager from session_handler.go**

In `internal/server/session_handler.go`:
- Remove `streams *stream.Manager` from SessionHandler
- Simplify `SwitchMode`: remove stream start/stop logic, keep only DB mode update (for now — handoff will replace this later)
- Remove stream imports

- [ ] **Step 3: Update session_handler_test.go**

Remove all tests that depend on stream.Manager. Update `setupHandler` to not require Manager.

- [ ] **Step 4: Remove stream package from main.go**

In `cmd/tbox/main.go`:
- Remove `stream.NewManager()` initialization
- Remove `sm.StopAll()` cleanup
- Update `server.New()` call to not pass Manager

- [ ] **Step 5: Delete stream package files**

```bash
rm -rf internal/stream/
rm internal/server/stream_handler.go internal/server/stream_handler_test.go
```

- [ ] **Step 6: Run all Go tests**

Run: `go test -race -count=1 ./...`
Expected: PASS (all remaining tests)

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "refactor: remove old stream subsystem (replaced by tbox relay)"
```

---

### Task 3: CLI Subcommand Infrastructure

Currently `cmd/tbox/main.go` has no subcommands. We need `tbox serve` (existing behavior) and `tbox relay` (new).

**Files:**
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: Refactor main.go to support subcommands**

```go
func main() {
    if len(os.Args) < 2 {
        fmt.Fprintf(os.Stderr, "Usage: tbox <command> [flags]\n")
        fmt.Fprintf(os.Stderr, "Commands: serve, relay\n")
        os.Exit(1)
    }

    switch os.Args[1] {
    case "serve":
        runServe(os.Args[2:])
    case "relay":
        runRelay(os.Args[2:])
    default:
        fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
        os.Exit(1)
    }
}

func runServe(args []string) {
    // Move existing main() logic here
    fs := flag.NewFlagSet("serve", flag.ExitOnError)
    cfgPath := fs.String("config", defaultConfigPath(), "path to config.toml")
    bindOverride := fs.String("bind", "", "override bind address")
    portOverride := fs.Int("port", 0, "override port")
    fs.Parse(args)
    // ... rest of existing serve logic
}

func runRelay(args []string) {
    // Placeholder — implemented in Task 4
    fmt.Fprintln(os.Stderr, "relay: not yet implemented")
    os.Exit(1)
}
```

- [ ] **Step 2: Verify `tbox serve` still works**

Run: `go build -o bin/tbox ./cmd/tbox && bin/tbox serve -config /dev/null &; sleep 1; curl -s http://127.0.0.1:7860/api/sessions; kill %1`
Expected: JSON response (empty array or session list)

- [ ] **Step 3: Commit**

```bash
git add cmd/tbox/main.go
git commit -m "refactor: add subcommand infrastructure (serve, relay)"
```

---

### Task 4: Implement `tbox relay` Command

**Files:**
- Create: `internal/relay/relay.go`
- Create: `internal/relay/relay_test.go`
- Modify: `cmd/tbox/main.go` — wire up `runRelay`

- [ ] **Step 1: Write failing test for relay bridge logic**

```go
// internal/relay/relay_test.go
func TestRelayBridgesStdinStdout(t *testing.T) {
    // Track messages received by mock daemon
    var received []string
    var mu sync.Mutex

    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        upgrader := websocket.Upgrader{}
        conn, _ := upgrader.Upgrade(w, r, nil)
        defer conn.Close()

        // Send a message to relay (simulating SPA user input)
        conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"user","message":"hello"}`))

        // Read what relay sends back (subprocess stdout)
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil { return }
            mu.Lock()
            received = append(received, string(msg))
            mu.Unlock()
        }
    }))
    defer srv.Close()

    wsURL := "ws" + srv.URL[4:]

    // Use "cat" as subprocess — it echoes stdin lines to stdout
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()

    r := &Relay{
        SessionName: "test",
        DaemonURL:   wsURL,
        Token:       "",
        Command:     []string{"cat"},
    }

    errCh := make(chan error, 1)
    go func() { errCh <- r.Run(ctx) }()

    // Wait for data to flow: WS → cat stdin → cat stdout → WS
    time.Sleep(500 * time.Millisecond)
    cancel()
    <-errCh

    mu.Lock()
    defer mu.Unlock()
    if len(received) == 0 {
        t.Fatal("expected relay to bridge at least one message, got none")
    }
    // cat echoes the input line back, so we should see it
    if received[0] != `{"type":"user","message":"hello"}` {
        t.Fatalf("unexpected message: %q", received[0])
    }
}

func TestRelayNoCommand(t *testing.T) {
    r := &Relay{Command: nil}
    err := r.Run(context.Background())
    if err == nil || err.Error() != "no command specified" {
        t.Fatalf("expected 'no command specified', got %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/relay/ -run TestRelay -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement relay.go**

```go
// internal/relay/relay.go
package relay

import (
    "bufio"
    "context"
    "fmt"
    "net/http"
    "os"
    "os/exec"
    "sync"
    "syscall"

    "github.com/gorilla/websocket"
)

type Relay struct {
    SessionName string
    DaemonURL   string
    Token       string
    Command     []string
}

func (r *Relay) Run(ctx context.Context) error {
    if len(r.Command) == 0 {
        return fmt.Errorf("no command specified")
    }

    // Connect to daemon WebSocket
    header := http.Header{}
    if r.Token != "" {
        header.Set("Authorization", "Bearer "+r.Token)
    }
    conn, _, err := websocket.DefaultDialer.DialContext(ctx, r.DaemonURL, header)
    if err != nil {
        return fmt.Errorf("connect to daemon: %w", err)
    }
    defer conn.Close()

    // Start subprocess
    cmd := exec.CommandContext(ctx, r.Command[0], r.Command[1:]...)
    stdin, err := cmd.StdinPipe()
    if err != nil {
        return fmt.Errorf("stdin pipe: %w", err)
    }
    stdout, err := cmd.StdoutPipe()
    if err != nil {
        return fmt.Errorf("stdout pipe: %w", err)
    }
    cmd.Stderr = os.Stderr // relay's own stderr + subprocess stderr

    if err := cmd.Start(); err != nil {
        return fmt.Errorf("start command: %w", err)
    }

    var wg sync.WaitGroup
    done := make(chan struct{})

    // Subprocess stdout → line-buffered tee to stderr + send to daemon WS
    // IMPORTANT: use bufio.Scanner for line-based reading to preserve NDJSON boundaries
    wg.Add(1)
    go func() {
        defer wg.Done()
        defer conn.WriteMessage(websocket.CloseMessage,
            websocket.FormatCloseMessage(websocket.CloseNormalClosure, "subprocess exited"))
        scanner := bufio.NewScanner(stdout)
        scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB max line
        for scanner.Scan() {
            line := scanner.Bytes()
            os.Stderr.Write(line)           // tee to terminal
            os.Stderr.Write([]byte("\n"))
            conn.WriteMessage(websocket.TextMessage, line)
        }
    }()

    // Daemon WS → subprocess stdin
    wg.Add(1)
    go func() {
        defer wg.Done()
        defer stdin.Close()
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil {
                // WS disconnected — send SIGTERM to subprocess
                if cmd.Process != nil {
                    cmd.Process.Signal(syscall.SIGTERM)
                }
                return
            }
            // Check for shutdown signal
            if len(msg) > 0 && msg[0] == '{' {
                // Quick check for shutdown message
                if string(msg) == `{"type":"shutdown"}` {
                    // Send SIGTERM to subprocess
                    if cmd.Process != nil {
                        cmd.Process.Signal(os.Interrupt)
                    }
                    return
                }
            }
            stdin.Write(msg)
            stdin.Write([]byte("\n"))
        }
    }()

    // Wait for subprocess to finish
    cmdErr := cmd.Wait()
    close(done)
    wg.Wait()

    if cmdErr != nil {
        fmt.Fprintf(os.Stderr, "tbox relay: subprocess exited: %v\n", cmdErr)
    }
    return cmdErr
}
```

- [ ] **Step 4: Wire up in main.go**

```go
func runRelay(args []string) {
    fs := flag.NewFlagSet("relay", flag.ExitOnError)
    session := fs.String("session", "", "session name (required)")
    daemon := fs.String("daemon", "ws://127.0.0.1:7860", "daemon WebSocket address")
    fs.Parse(args)

    if *session == "" {
        fmt.Fprintln(os.Stderr, "relay: --session is required")
        os.Exit(1)
    }

    // Everything after "--" is the command
    cmdArgs := fs.Args()
    if len(cmdArgs) == 0 {
        fmt.Fprintln(os.Stderr, "relay: no command specified after --")
        os.Exit(1)
    }

    token := os.Getenv("TBOX_TOKEN")
    wsURL := fmt.Sprintf("%s/ws/cli-bridge/%s", *daemon, *session)

    r := &relay.Relay{
        SessionName: *session,
        DaemonURL:   wsURL,
        Token:       token,
        Command:     cmdArgs,
    }

    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer cancel()

    if err := r.Run(ctx); err != nil {
        fmt.Fprintf(os.Stderr, "relay: %v\n", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/relay/ -v && go build ./cmd/tbox`
Expected: PASS + build success

- [ ] **Step 6: Commit**

```bash
git add internal/relay/ cmd/tbox/main.go
git commit -m "feat: add tbox relay command for bridging subprocess to daemon"
```

---

### Task 5: CLI Bridge WebSocket Endpoint

**Files:**
- Create: `internal/bridge/bridge.go`
- Create: `internal/bridge/bridge_test.go`
- Modify: `internal/server/server.go` — add route + Bridge dependency

- [ ] **Step 1: Write failing test for bridge fan-out**

```go
// internal/bridge/bridge_test.go
func TestBridgeFanOut(t *testing.T) {
    b := bridge.New()

    // Simulate relay connecting
    relayCh := b.RegisterRelay("test-session")
    defer b.UnregisterRelay("test-session")

    // Simulate two SPA subscribers
    id1, sub1 := b.Subscribe("test-session")
    id2, sub2 := b.Subscribe("test-session")
    defer b.Unsubscribe("test-session", id1)
    defer b.Unsubscribe("test-session", id2)

    // Relay sends data (simulating subprocess stdout)
    b.RelayToSubscribers("test-session", []byte(`{"type":"assistant"}`))

    // Both subscribers should receive
    select {
    case msg := <-sub1:
        if string(msg) != `{"type":"assistant"}` {
            t.Fatalf("sub1 got %q", msg)
        }
    case <-time.After(time.Second):
        t.Fatal("sub1 timeout")
    }

    select {
    case msg := <-sub2:
        if string(msg) != `{"type":"assistant"}` {
            t.Fatalf("sub2 got %q", msg)
        }
    case <-time.After(time.Second):
        t.Fatal("sub2 timeout")
    }

    // SPA sends message — should arrive at relay channel
    b.SubscriberToRelay("test-session", []byte(`{"type":"user"}`))

    select {
    case msg := <-relayCh:
        if string(msg) != `{"type":"user"}` {
            t.Fatalf("relay got %q", msg)
        }
    case <-time.After(time.Second):
        t.Fatal("relay timeout")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/bridge/ -run TestBridge -v`
Expected: FAIL — package not found

- [ ] **Step 3: Implement bridge.go**

```go
// internal/bridge/bridge.go
package bridge

import "sync"

type Bridge struct {
    mu       sync.RWMutex
    sessions map[string]*sessionBridge
}

type sessionBridge struct {
    relayCh     chan []byte             // messages from SPA → relay
    subscribers map[uint64]chan []byte   // id → channel, messages from relay → SPA
    nextID      uint64
}

func New() *Bridge {
    return &Bridge{sessions: make(map[string]*sessionBridge)}
}

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

func (b *Bridge) HasRelay(name string) bool {
    b.mu.RLock()
    defer b.mu.RUnlock()
    _, ok := b.sessions[name]
    return ok
}

// Unsubscribe takes the opaque ID returned by Subscribe.
// Internally, subscribers are tracked by a unique uint64 ID to avoid
// channel type comparison issues (chan vs <-chan).
// NOTE: Refactor Subscribe/Unsubscribe to use an ID-based approach:
//   Subscribe returns (id uint64, ch <-chan []byte)
//   Unsubscribe takes (name string, id uint64)
// This avoids the Go type system issue where chan[]byte != <-chan[]byte.

type SubID uint64

func (b *Bridge) Subscribe(name string) (SubID, <-chan []byte) {
    b.mu.Lock()
    defer b.mu.Unlock()
    sb, ok := b.sessions[name]
    if !ok {
        return 0, nil
    }
    sb.nextID++
    id := sb.nextID
    ch := make(chan []byte, 64)
    sb.subscribers[id] = ch
    return SubID(id), ch
}

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
        default: // drop if subscriber is slow
        }
    }
}

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
```

- [ ] **Step 4: Add WebSocket handler and route**

Create `internal/server/bridge_handler.go`:

**Design note:** Spec defines a single `/ws/cli-bridge/{session}` endpoint. Implementation splits into two endpoints: `/ws/cli-bridge/{session}` for relay (producer) and `/ws/cli-bridge-sub/{session}` for SPA (consumer). This separation clarifies the role of each connection and prevents SPA clients from accidentally registering as relays.

```go
package server

import (
    "net/http"
    "github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}

// handleCliBridge handles WebSocket from tbox relay (producer).
// Only one relay per session at a time.
func (s *Server) handleCliBridge(w http.ResponseWriter, r *http.Request) {
    sessionName := r.PathValue("session")
    if s.bridge.HasRelay(sessionName) {
        http.Error(w, "relay already connected", http.StatusConflict)
        return
    }

    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil { return }
    defer conn.Close()

    relayCh := s.bridge.RegisterRelay(sessionName)
    defer s.bridge.UnregisterRelay(sessionName)

    // Notify session-events subscribers
    s.events.Broadcast(sessionName, "handoff", "connected")

    done := make(chan struct{})

    // Relay WS → bridge (subprocess stdout → SPA subscribers)
    go func() {
        defer close(done)
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil { return }
            s.bridge.RelayToSubscribers(sessionName, msg)
        }
    }()

    // Bridge → relay WS (SPA user input → subprocess stdin)
    go func() {
        for msg := range relayCh {
            if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
                return
            }
        }
    }()

    <-done
    s.events.Broadcast(sessionName, "handoff", "disconnected")
}

// handleCliBridgeSubscribe handles WebSocket from SPA clients (consumer).
func (s *Server) handleCliBridgeSubscribe(w http.ResponseWriter, r *http.Request) {
    sessionName := r.PathValue("session")
    if !s.bridge.HasRelay(sessionName) {
        http.Error(w, "no relay connected", http.StatusNotFound)
        return
    }

    conn, err := upgrader.Upgrade(w, r, nil)
    if err != nil { return }
    defer conn.Close()

    id, subCh := s.bridge.Subscribe(sessionName)
    if subCh == nil {
        return
    }
    defer s.bridge.Unsubscribe(sessionName, id)

    done := make(chan struct{})

    // Bridge → SPA WS (relay output → browser)
    go func() {
        defer close(done)
        for msg := range subCh {
            if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
                return
            }
        }
    }()

    // SPA WS → bridge (user input → relay stdin)
    go func() {
        for {
            _, msg, err := conn.ReadMessage()
            if err != nil { return }
            s.bridge.SubscriberToRelay(sessionName, msg)
        }
    }()

    <-done
}
```

Also create `internal/server/bridge_handler_test.go` with WebSocket integration tests using `httptest.NewServer`.

Add routes in `server.go`:
```go
s.mux.HandleFunc("/ws/cli-bridge/{session}", s.handleCliBridge)
s.mux.HandleFunc("/ws/cli-bridge-sub/{session}", s.handleCliBridgeSubscribe)
```

Add `bridge *bridge.Bridge` and `events *EventsBroadcaster` fields to Server.

- [ ] **Step 5: Run all tests**

Run: `go test -race -count=1 ./...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bridge/ internal/server/bridge_handler.go internal/server/server.go cmd/tbox/main.go
git commit -m "feat: add cli-bridge WebSocket endpoint with fan-out"
```

---

### Task 6: Session Status Detection

**Files:**
- Create: `internal/detect/detector.go`
- Create: `internal/detect/detector_test.go`
- Modify: `internal/tmux/executor.go` — add `CapturePaneContent` and `PaneCurrentCommand` methods

- [ ] **Step 1: Extend tmux Executor interface**

Add to `internal/tmux/executor.go`:

```go
type Executor interface {
    // ... existing methods
    PaneCurrentCommand(target string) (string, error)
    CapturePaneContent(target string, lastN int) (string, error)
}
```

Implement for `RealExecutor`:
- `PaneCurrentCommand`: runs `tmux list-panes -t {target} -F '#{pane_current_command}'`
- `CapturePaneContent`: runs `tmux capture-pane -t {target} -p -S -{lastN}`

Add to `FakeExecutor`:
```go
type FakeExecutor struct {
    // ... existing fields
    paneCommands map[string]string  // target → command name
    paneContents map[string]string  // target → captured text
}

func (f *FakeExecutor) SetPaneCommand(target, cmd string) { ... }
func (f *FakeExecutor) SetPaneContent(target, content string) { ... }
func (f *FakeExecutor) PaneCurrentCommand(target string) (string, error) { ... }
func (f *FakeExecutor) CapturePaneContent(target string, lastN int) (string, error) { ... }
```

- [ ] **Step 2: Write failing test for detector**

```go
// internal/detect/detector_test.go
func TestDetectStatus(t *testing.T) {
    fake := tmux.NewFakeExecutor()
    d := detect.New(fake, []string{"claude", "cld"})

    tests := []struct {
        name     string
        cmd      string
        content  string
        expected detect.Status
    }{
        {"shell idle", "zsh", "", detect.StatusNormal},
        {"non-cc command", "node", "", detect.StatusNotInCC},
        {"cc idle", "claude", "❯ ", detect.StatusCCIdle},
        {"cc running", "claude", "⠋ Reading file...", detect.StatusCCRunning},
        {"cc waiting permission", "claude", "Allow  Deny", detect.StatusCCWaiting},
        {"cc alias idle", "cld", "❯ ", detect.StatusCCIdle},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            fake.SetPaneCommand("test", tt.cmd)
            fake.SetPaneContent("test", tt.content)
            status := d.Detect("test")
            if status != tt.expected {
                t.Fatalf("expected %s, got %s", tt.expected, status)
            }
        })
    }
}
```

- [ ] **Step 3: Implement detector.go**

```go
package detect

type Status string

const (
    StatusNormal    Status = "normal"
    StatusNotInCC   Status = "not-in-cc"
    StatusCCIdle    Status = "cc-idle"
    StatusCCRunning Status = "cc-running"
    StatusCCWaiting Status = "cc-waiting"
    StatusCCUnread  Status = "cc-unread"
    // Note: cc-unread is NOT detected by tmux polling. It is set by the
    // bridge layer when new assistant messages arrive while no SPA client
    // is subscribed to the session's cli-bridge-sub WebSocket.
    // The polling goroutine (Task 7) tracks this separately.
)

// PTY instant detection (spec method A) is deferred to a follow-up task.
// The tmux polling approach (method B) provides 1-2s latency which is
// acceptable for v0.3.0. Method A can be added later by hooking into
// terminal/relay.go's PTY read loop to detect CC patterns in real-time.

type Detector struct {
    tmux       tmux.Executor
    ccCommands []string
}

func New(tmux tmux.Executor, ccCommands []string) *Detector { ... }
func (d *Detector) Detect(session string) Status { ... }
```

Pattern matching logic:
- `pane_current_command` not in ccCommands → shell command → `StatusNormal` (if shell) or `StatusNotInCC`
- In ccCommands → capture pane → check patterns:
  - `❯` at end → `StatusCCIdle`
  - `Allow / Deny` or `?` prompt → `StatusCCWaiting`
  - Otherwise → `StatusCCRunning`

- [ ] **Step 4: Run tests**

Run: `go test ./internal/detect/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/detect/ internal/tmux/executor.go
git commit -m "feat: add session status detector via tmux polling"
```

---

### Task 7: Session Events WebSocket + Status Polling

**Files:**
- Create: `internal/server/events_handler.go`
- Modify: `internal/server/server.go` — add route + start polling goroutine

- [ ] **Step 1: Implement events handler**

`/ws/session-events` endpoint — SPA subscribes to receive status changes and handoff notifications.

Message format:
```json
{"type": "status", "session": "myproject", "status": "cc-running"}
{"type": "handoff", "session": "myproject", "state": "connected"}
```

- [ ] **Step 2: Add status polling goroutine in server**

On server start, spawn goroutine that:
- Every `config.Detect.PollInterval` seconds
- For each session with active WS subscribers
- Calls `detector.Detect(session)`
- If status changed, broadcast to session-events subscribers

- [ ] **Step 3: Run all tests**

Run: `go test -race -count=1 ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/events_handler.go internal/server/server.go
git commit -m "feat: add session-events WebSocket with status polling"
```

---

### Task 8: Handoff API Endpoint

**Files:**
- Create: `internal/server/handoff_handler.go`
- Create: `internal/server/handoff_handler_test.go`
- Modify: `internal/server/server.go` — add route
- Modify: `internal/server/session_handler.go` — update SwitchMode to delegate to handoff

- [ ] **Step 1: Write failing test for handoff**

Test the happy path: session in `normal` state → handoff → tbox relay command sent to tmux.

- [ ] **Step 2: Implement handoff handler**

```go
// POST /api/sessions/{id}/handoff
// Body: {"mode": "stream", "preset": "cc"}
// Response: 202 Accepted {"handoff_id": "uuid"}
//
// Runs async in goroutine. Progress pushed via /ws/session-events:
//   {"type":"handoff","session":"X","state":"detecting"}
//   {"type":"handoff","session":"X","state":"stopping-cc"}
//   {"type":"handoff","session":"X","state":"launching"}
//   {"type":"handoff","session":"X","state":"connected"} or {"state":"failed","error":"..."}
//
// Flow:
// 1. Lock per-session mutex (tryLock, fail immediately if already locked)
// 2. Generate handoff_id, return 202 immediately
// 3. In goroutine:
//    a. Lookup preset command from config
//    b. If relay already connected for this session → send {"type":"shutdown"} via cli-bridge → wait for disconnect (5s)
//    c. Detect current tmux state via detector
//    d. If CC running → tmux send-keys C-c → poll pane_current_command until shell (10s timeout)
//    e. If timeout → broadcast failed, unlock, return
//    f. tmux send-keys "TBOX_TOKEN={token} tbox relay --session {name} -- {command}" Enter
//    g. Wait for relay to connect to cli-bridge (15s timeout)
//    h. Broadcast result (connected/failed), unlock
```

- [ ] **Step 3: Update SwitchMode**

`SwitchMode` now only updates DB mode field. Stream start/stop is handled by handoff.

- [ ] **Step 4: Run all tests**

Run: `go test -race -count=1 ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/server/handoff_handler.go internal/server/handoff_handler_test.go internal/server/session_handler.go internal/server/server.go
git commit -m "feat: add handoff API with auto CC detection and relay launch"
```

---

### Task 9: Config API

**Files:**
- Create: `internal/server/config_handler.go`
- Create: `internal/server/config_handler_test.go`
- Modify: `internal/server/server.go` — add routes

- [ ] **Step 1: Write failing test**

Test `GET /api/config` returns config without token field. Test `PUT /api/config` updates presets.

- [ ] **Step 2: Implement config handler**

```go
// GET /api/config — returns config (token field redacted)
// PUT /api/config — accepts partial config update, writes to config.toml
```

Uses `sync.RWMutex` on config to handle concurrent reads/writes. Config reload after write.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/server/ -run TestConfig -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/config_handler.go internal/server/config_handler_test.go internal/server/server.go
git commit -m "feat: add config API for reading/updating presets and detect settings"
```

---

## Phase B: SPA UI Redesign

### Task 10: Style Foundation + MessageBubble Rewrite

**Files:**
- Modify: `spa/src/index.css` — global brightness adjustment
- Modify: `spa/src/components/MessageBubble.tsx`
- Modify: `spa/src/components/MessageBubble.test.tsx`

- [ ] **Step 1: Update failing test for MessageBubble**

Update test expectations: no avatar icons, no `icon-user`/`icon-assistant` test IDs.

- [ ] **Step 2: Rewrite MessageBubble**

Remove avatar (User/Robot icons), remove `flex-row-reverse`. User bubble right-aligned, assistant left-aligned. No identity markers. Update colors: user `bg-blue-700`, assistant `bg-[#2a2f38]`.

- [ ] **Step 3: Adjust global brightness in index.css**

Background from `#111` → `#191919`. Adjust Tailwind theme overrides as needed.

- [ ] **Step 4: Run SPA tests**

Run: `cd spa && npx vitest run`
Expected: PASS (with updated test expectations)

- [ ] **Step 5: Commit**

```bash
git add spa/src/components/MessageBubble.tsx spa/src/components/MessageBubble.test.tsx spa/src/index.css
git commit -m "feat(spa): rewrite MessageBubble — remove avatars, update colors"
```

---

### Task 11: ThinkingIndicator + StreamInput Rewrite

**Files:**
- Create: `spa/src/components/ThinkingIndicator.tsx`
- Create: `spa/src/components/ThinkingIndicator.test.tsx`
- Modify: `spa/src/components/StreamInput.tsx`
- Modify: `spa/src/components/StreamInput.test.tsx`

- [ ] **Step 1: Write failing test for ThinkingIndicator**

Test renders three animated dots, hidden when `visible=false`.

- [ ] **Step 2: Implement ThinkingIndicator**

Three blue-400 pulsing dots in assistant bubble container.

- [ ] **Step 3: Rewrite StreamInput as card-style**

Card container with: textarea (auto-grow, Enter send, Shift+Enter newline), toolbar below (+ button for attachments). No send button.

- [ ] **Step 4: Update StreamInput test**

- [ ] **Step 5: Run tests**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/ThinkingIndicator.tsx spa/src/components/ThinkingIndicator.test.tsx spa/src/components/StreamInput.tsx spa/src/components/StreamInput.test.tsx
git commit -m "feat(spa): add ThinkingIndicator, rewrite StreamInput as card"
```

---

### Task 12: PermissionPrompt + AskUserQuestion Rewrite

**Files:**
- Modify: `spa/src/components/PermissionPrompt.tsx`
- Modify: `spa/src/components/PermissionPrompt.test.tsx`
- Modify: `spa/src/components/AskUserQuestion.tsx`
- Modify: `spa/src/components/AskUserQuestion.test.tsx`

- [ ] **Step 1: Rewrite PermissionPrompt**

Horizontal layout: left side (icon + tool name + description), right side (Allow/Deny buttons).

- [ ] **Step 2: Rewrite AskUserQuestion**

Remove Submit/Cancel buttons. Options: click to select + Enter to confirm. Free-text: input field + Enter to submit. Both variants based on whether `options` array is empty.

- [ ] **Step 3: Update tests**

- [ ] **Step 4: Run tests + commit**

```bash
git add spa/src/components/PermissionPrompt.* spa/src/components/AskUserQuestion.*
git commit -m "feat(spa): rewrite PermissionPrompt and AskUserQuestion"
```

---

### Task 13: FileAttachment Component

**Files:**
- Create: `spa/src/components/FileAttachment.tsx`
- Create: `spa/src/components/FileAttachment.test.tsx`

- [ ] **Step 1: Write failing test**

Test: renders attachment items, × removes item, drag-drop overlay shows.

- [ ] **Step 2: Implement FileAttachment**

Floating above input card. Supports: drag-drop (full-screen overlay), + button file picker, image thumbnail preview, × remove. Files stored as `{ name, type, url(base64) }`.

- [ ] **Step 3: Run tests + commit**

```bash
git add spa/src/components/FileAttachment.*
git commit -m "feat(spa): add FileAttachment with drag-drop and preview"
```

---

### Task 14: Connection Layer + Store Changes

**Files:**
- Modify: `spa/src/lib/stream-ws.ts` — update URL pattern
- Modify: `spa/src/lib/api.ts` — add handoff + config API calls
- Modify: `spa/src/stores/useStreamStore.ts` — add handoffState
- Create: `spa/src/lib/session-events.ts` — new session-events WS client
- Create: `spa/src/stores/useConfigStore.ts` — config/presets state

- [ ] **Step 1: Update stream-ws.ts**

Change WebSocket URL from `/ws/stream/{session}` to `/ws/cli-bridge-sub/{session}`. Protocol unchanged.

- [ ] **Step 2: Add API functions**

```typescript
// api.ts additions
export async function handoff(base: string, id: number, mode: string, preset: string): Promise<{handoff_id: string}> { ... }
export async function getConfig(base: string): Promise<Config> { ... }
export async function updateConfig(base: string, updates: Partial<Config>): Promise<Config> { ... }
```

- [ ] **Step 3: Add session-events.ts**

```typescript
export function connectSessionEvents(url: string, onEvent: (event: SessionEvent) => void): EventConnection { ... }
```

- [ ] **Step 4: Update useStreamStore**

Add `handoffState: 'idle' | 'handoff-in-progress' | 'connected' | 'disconnected'`.

- [ ] **Step 5: Create useConfigStore**

Zustand store for presets and detect config. Fetches from daemon on mount.

- [ ] **Step 6: Run tests + commit**

```bash
git add spa/src/lib/ spa/src/stores/
git commit -m "feat(spa): update connection layer for cli-bridge, add config store"
```

---

### Task 15: HandoffButton Component

**Files:**
- Create: `spa/src/components/HandoffButton.tsx`
- Create: `spa/src/components/HandoffButton.test.tsx`

- [ ] **Step 1: Write failing test**

```typescript
// HandoffButton.test.tsx
test('renders handoff button with preset name', () => {
  render(<HandoffButton preset="cc" state="idle" onHandoff={() => {}} />)
  expect(screen.getByText(/cc/)).toBeInTheDocument()
})

test('shows progress when handoff in progress', () => {
  render(<HandoffButton preset="cc" state="handoff-in-progress" onHandoff={() => {}} />)
  expect(screen.getByText(/connecting/i)).toBeInTheDocument()
})

test('calls onHandoff when clicked', async () => {
  const onHandoff = vi.fn()
  render(<HandoffButton preset="cc" state="idle" onHandoff={onHandoff} />)
  await userEvent.click(screen.getByRole('button'))
  expect(onHandoff).toHaveBeenCalled()
})
```

- [ ] **Step 2: Implement HandoffButton**

```tsx
interface Props {
  preset: string
  state: 'idle' | 'handoff-in-progress' | 'connected' | 'disconnected'
  onHandoff: () => void
}

export default function HandoffButton({ preset, state, onHandoff }: Props) {
  if (state === 'connected') return null

  return (
    <div className="flex items-center justify-center h-full">
      <button
        onClick={onHandoff}
        disabled={state === 'handoff-in-progress'}
        className="px-4 py-2 rounded-lg bg-blue-600 text-white disabled:opacity-50"
      >
        {state === 'handoff-in-progress' ? 'Connecting...' : `Start ${preset}`}
      </button>
      {state === 'disconnected' && (
        <p className="text-sm text-gray-500 mt-2">Session disconnected. Click to reconnect.</p>
      )}
    </div>
  )
}
```

- [ ] **Step 3: Run tests + commit**

```bash
git add spa/src/components/HandoffButton.*
git commit -m "feat(spa): add HandoffButton component"
```

---

### Task 16: ConversationView Integration

**Files:**
- Modify: `spa/src/components/ConversationView.tsx`
- Modify: `spa/src/components/ConversationView.test.tsx`

- [ ] **Step 1: Integrate new components**

- Add ThinkingIndicator (show after send, hide on first assistant message)
- Add FileAttachment above input card
- Wire to new WebSocket URL (`/ws/cli-bridge-sub/`)
- Add HandoffButton (show when not connected)

- [ ] **Step 2: Update tests**

- [ ] **Step 3: Run tests + commit**

```bash
git add spa/src/components/ConversationView.*
git commit -m "feat(spa): integrate ThinkingIndicator, FileAttachment, HandoffButton in ConversationView"
```

---

### Task 17: TopBar Preset Dropdowns

**Files:**
- Modify: `spa/src/components/TopBar.tsx`
- Modify: `spa/src/components/TopBar.test.tsx`

- [ ] **Step 1: Rewrite TopBar**

- stream/jsonl buttons become dropdown triggers
- Dropdown shows presets from `useConfigStore`
- Single preset → direct click (no dropdown)
- Multiple presets → dropdown on click
- Click preset → call `handoff(base, id, mode, preset.name)`
- Active preset highlighted

- [ ] **Step 2: Update tests**

- [ ] **Step 3: Run tests + commit**

```bash
git add spa/src/components/TopBar.*
git commit -m "feat(spa): add preset dropdowns to TopBar"
```

---

### Task 18: SessionStatusBadge

**Files:**
- Create: `spa/src/components/SessionStatusBadge.tsx`
- Create: `spa/src/components/SessionStatusBadge.test.tsx`
- Modify: `spa/src/components/SessionPanel.tsx`

- [ ] **Step 1: Implement SessionStatusBadge**

Small colored dot next to session name. Colors: normal/not-in-cc=灰, cc-idle=灰綠, cc-running=綠, cc-waiting=黃, cc-unread=藍.

- [ ] **Step 2: Wire into SessionPanel**

Connect to session-events WebSocket, display badge per session.

- [ ] **Step 3: Run tests + commit**

```bash
git add spa/src/components/SessionStatusBadge.* spa/src/components/SessionPanel.tsx
git commit -m "feat(spa): add SessionStatusBadge to SessionPanel"
```

---

### Task 19: App.tsx Integration + Cleanup

**Files:**
- Modify: `spa/src/App.tsx`
- Modify: `spa/src/components/ToolCallBlock.tsx` — style tweaks

- [ ] **Step 1: Update App.tsx**

- Connect session-events WS on mount
- Pass handoff functions to ConversationView
- Remove old switchMode calls (replaced by handoff)
- ToolCallBlock: adjust colors to match new palette

- [ ] **Step 2: Run full test suite**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add spa/src/App.tsx spa/src/components/ToolCallBlock.tsx
git commit -m "feat(spa): integrate all new components in App, style tweaks"
```

---

## Phase C: End-to-End Verification

### Task 20: Settings Panel

**Files:**
- Create: `spa/src/components/SettingsPanel.tsx`
- Create: `spa/src/components/SettingsPanel.test.tsx`
- Modify: `spa/src/App.tsx` — add settings toggle/route

- [ ] **Step 1: Write failing test**

```typescript
test('renders stream presets list', async () => {
  // Mock useConfigStore with presets
  render(<SettingsPanel />)
  expect(screen.getByText('Stream Presets')).toBeInTheDocument()
  expect(screen.getByText('cc')).toBeInTheDocument()
})

test('can add a new preset', async () => { ... })
test('can edit cc_commands', async () => { ... })
test('can delete a preset', async () => { ... })
```

- [ ] **Step 2: Implement SettingsPanel**

Panel/modal with sections:
- **Stream Presets**: list with name + command, add/edit/delete buttons
- **JSONL Presets**: same as stream (disabled note: "Phase 3")
- **CC Detection**: editable list of `cc_commands`, number input for `poll_interval`
- All changes go through `useConfigStore.updateConfig()` → `PUT /api/config`

- [ ] **Step 3: Wire into App.tsx**

Add a settings gear icon in TopBar or SessionPanel that toggles SettingsPanel overlay.

- [ ] **Step 4: Run tests + commit**

```bash
git add spa/src/components/SettingsPanel.* spa/src/App.tsx
git commit -m "feat(spa): add SettingsPanel for preset and detect config CRUD"
```

---

### Task 21: Integration Test

- [ ] **Step 1: Run full Go test suite**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test -race -count=1 ./...`
Expected: PASS

- [ ] **Step 2: Run full SPA test suite**

Run: `cd spa && npx vitest run`
Expected: PASS

- [ ] **Step 3: Build both**

Run: `make build && cd spa && npm run build`
Expected: SUCCESS

- [ ] **Step 4: Manual smoke test**

1. Start daemon: `bin/tbox serve`
2. Open SPA in browser
3. Select a tmux session → click stream preset → verify handoff
4. Send a message → verify thinking indicator → verify response
5. Switch to term tab → verify `tbox relay` is visible
6. Ctrl+C in term → verify stream disconnects
7. Upload a file → verify attachment preview

- [ ] **Step 5: Commit any fixes**

---

### Task 22: Update CLAUDE.md + Memory

- [ ] **Step 1: Update CLAUDE.md project description**

- [ ] **Step 2: Update version to 0.3.0 in relevant files**

- [ ] **Step 3: Final commit**

```bash
git commit -m "docs: update project docs for v0.3.0 stream redesign"
```
