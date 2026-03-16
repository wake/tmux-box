# Phase 1: Daemon 基礎 + Terminal 模式 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立可從瀏覽器連到 tmux session 的最小可用版本 — Go daemon 提供 REST + WebSocket API，React SPA 透過 xterm.js 渲染終端畫面，含 PTY resize。

**Architecture:** Daemon 和 SPA 完全分離。Go daemon 是純 API server，負責 config 載入、SQLite 持久化、tmux 操作、PTY 中繼（含 resize）。React SPA 獨立部署，透過 WebSocket 連接 daemon 取得終端 I/O，透過 REST API 管理 session。

**Tech Stack:**
- Daemon: Go 1.26 / net/http ServeMux / gorilla/websocket / creack/pty / modernc.org/sqlite / BurntSushi/toml
- SPA: React 19 / Vite / xterm.js / Zustand / Tailwind CSS / vitest

**前置條件:** Go 1.26+, pnpm, tmux, Node.js 24+

**命名:**
- 專案/repo: tmux-box
- CLI binary: `tbox`
- Config: `~/.config/tbox/`
- Go module: `github.com/wake/tmux-box`

**不在 Phase 1 範圍:** JSONL 模式、Stream 模式、檔案瀏覽/編輯、多主機聚合、群組管理、Electron 封裝、`tbox auth` 金鑰組（Phase 1 用簡單 token）

---

## File Structure

```
tmux-box/
├── cmd/tbox/
│   └── main.go                      # CLI 入口
├── internal/
│   ├── config/
│   │   ├── config.go                # Config struct + TOML 載入
│   │   └── config_test.go
│   ├── store/
│   │   ├── store.go                 # SQLite schema + CRUD
│   │   └── store_test.go
│   ├── tmux/
│   │   ├── executor.go              # tmux CLI 指令執行器（介面 + 實作 + fake）
│   │   └── executor_test.go
│   ├── server/
│   │   ├── server.go                # HTTP server 組裝 + CORS
│   │   ├── middleware.go            # IP 白名單 + token 認證
│   │   ├── middleware_test.go
│   │   ├── session_handler.go       # Session REST API
│   │   └── session_handler_test.go
│   └── terminal/
│       ├── relay.go                 # WebSocket ↔ PTY 中繼（含 resize）
│       ├── relay_test.go
│       ├── batcher.go               # 輸出批次化
│       └── batcher_test.go
├── spa/                             # React SPA（獨立部署）
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── index.html
│   └── src/
│       ├── main.tsx
│       ├── App.tsx
│       ├── components/
│       │   ├── TerminalView.tsx
│       │   ├── TerminalView.test.tsx
│       │   ├── SessionPanel.tsx
│       │   └── SessionPanel.test.tsx
│       ├── stores/
│       │   ├── useSessionStore.ts
│       │   └── useSessionStore.test.ts
│       └── lib/
│           ├── api.ts
│           ├── api.test.ts
│           └── ws.ts
├── go.mod
├── go.sum
└── Makefile
```

---

## Chunk 1: Go 專案骨架 + Config + Store

### Task 1: Go 專案初始化

**Files:**
- Create: `go.mod`
- Create: `cmd/tbox/main.go`
- Create: `Makefile`
- Create: `.gitignore`

- [ ] **Step 1: 初始化 Go module + 建立骨架**

```bash
cd /Users/wake/Workspace/wake/tmux-box
go mod init github.com/wake/tmux-box
```

```go
// cmd/tbox/main.go
package main

import "fmt"

func main() {
	fmt.Println("tbox")
}
```

```makefile
# Makefile
.PHONY: build test lint clean

BIN := bin/tbox

build:
	go build -o $(BIN) ./cmd/tbox

test:
	go test -race -count=1 ./...

lint:
	go vet ./...

clean:
	rm -rf bin/
```

更新 `.gitignore`：

```
.superpowers/
bin/
spa/node_modules/
spa/dist/
```

- [ ] **Step 2: 驗證 build + run**

Run: `make build && ./bin/tbox`
Expected: 印出 `tbox`

- [ ] **Step 3: Commit**

```bash
git add go.mod cmd/tbox/main.go Makefile .gitignore
git commit -m "feat: init Go project scaffold with tbox CLI"
```

---

### Task 2: Config 載入

**Files:**
- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/config/config_test.go
package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/config"
)

func TestLoadDefaultsWhenFileNotExist(t *testing.T) {
	cfg, err := config.Load(filepath.Join(t.TempDir(), "nonexistent.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "127.0.0.1" {
		t.Errorf("bind: want 127.0.0.1, got %s", cfg.Bind)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

func TestLoadFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
bind = "100.64.0.2"
port = 9090
token = "secret123"
allow = ["10.0.0.0/8"]
`), 0644)

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bind != "100.64.0.2" {
		t.Errorf("bind: want 100.64.0.2, got %s", cfg.Bind)
	}
	if cfg.Port != 9090 {
		t.Errorf("port: want 9090, got %d", cfg.Port)
	}
	if cfg.Token != "secret123" {
		t.Errorf("token: want secret123, got %s", cfg.Token)
	}
	if len(cfg.Allow) != 1 || cfg.Allow[0] != "10.0.0.0/8" {
		t.Errorf("allow: want [10.0.0.0/8], got %v", cfg.Allow)
	}
}

func TestLoadAutoDefaultPath(t *testing.T) {
	// 確保不會讀到真實 config — 設定 HOME 到空目錄
	t.Setenv("HOME", t.TempDir())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 7860 {
		t.Errorf("port: want 7860, got %d", cfg.Port)
	}
}

func TestLoadInvalidTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.toml")
	os.WriteFile(path, []byte(`not valid toml {{{{`), 0644)

	_, err := config.Load(path)
	if err == nil {
		t.Error("want error for invalid TOML")
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/config/...`
Expected: FAIL — package 不存在

- [ ] **Step 3: 實作**

```go
// internal/config/config.go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Bind         string   `toml:"bind"`
	Port         int      `toml:"port"`
	Token        string   `toml:"token"`
	Allow        []string `toml:"allow"`
	DataDir      string   `toml:"data_dir"`
	AllowedPaths []string `toml:"allowed_paths"`
}

func defaults() Config {
	home, _ := os.UserHomeDir()
	return Config{
		Bind:    "127.0.0.1",
		Port:    7860,
		DataDir: filepath.Join(home, ".config", "tbox"),
	}
}

// Load reads config from path. Empty path → tries ~/.config/tbox/config.toml.
// Missing file → returns defaults (no error). Invalid TOML → returns error.
func Load(path string) (Config, error) {
	cfg := defaults()

	if path == "" {
		path = filepath.Join(cfg.DataDir, "config.toml")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go mod tidy && go test ./internal/config/...`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/config/ go.mod go.sum
git commit -m "feat: add config loading with TOML and auto default path"
```

---

### Task 3: SQLite Store

**Files:**
- Create: `internal/store/store.go`
- Create: `internal/store/store_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/store/store_test.go
package store_test

import (
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/store"
)

func openTestDB(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchema(t *testing.T) {
	openTestDB(t)
}

func TestSessionCRUD(t *testing.T) {
	db := openTestDB(t)

	// Create
	s := store.Session{Name: "myapp", TmuxTarget: "myapp:0", Cwd: "/home/user/myapp", Mode: "term"}
	id, err := db.CreateSession(s)
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Error("want non-zero id")
	}

	// List
	sessions, err := db.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Name != "myapp" {
		t.Errorf("list: want [myapp], got %v", sessions)
	}

	// Update
	err = db.UpdateSession(id, store.SessionUpdate{Name: ptr("renamed")})
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ = db.ListSessions()
	if sessions[0].Name != "renamed" {
		t.Errorf("update: want renamed, got %s", sessions[0].Name)
	}

	// Delete
	err = db.DeleteSession(id)
	if err != nil {
		t.Fatal(err)
	}
	sessions, _ = db.ListSessions()
	if len(sessions) != 0 {
		t.Errorf("delete: want 0 sessions, got %d", len(sessions))
	}
}

func TestDeleteNonexistent(t *testing.T) {
	db := openTestDB(t)
	err := db.DeleteSession(999)
	if err != store.ErrNotFound {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

func TestGroupCRUD(t *testing.T) {
	db := openTestDB(t)

	id, err := db.CreateGroup("AI Agents")
	if err != nil {
		t.Fatal(err)
	}

	groups, _ := db.ListGroups()
	if len(groups) != 1 || groups[0].Name != "AI Agents" {
		t.Errorf("list: want [AI Agents], got %v", groups)
	}

	db.UpdateGroup(id, "Renamed")
	groups, _ = db.ListGroups()
	if groups[0].Name != "Renamed" {
		t.Errorf("update: want Renamed, got %s", groups[0].Name)
	}
}

func ptr(s string) *string { return &s }
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/store/...`
Expected: FAIL

- [ ] **Step 3: 實作**

```go
// internal/store/store.go
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct{ db *sql.DB }

type Session struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TmuxTarget string `json:"tmux_target"`
	Cwd        string `json:"cwd"`
	Mode       string `json:"mode"`
	GroupID    int64  `json:"group_id"`
	SortOrder  int    `json:"sort_order"`
}

type SessionUpdate struct {
	Name    *string `json:"name,omitempty"`
	Mode    *string `json:"mode,omitempty"`
	GroupID *int64  `json:"group_id,omitempty"`
}

type Group struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	SortOrder int    `json:"sort_order"`
	Collapsed bool   `json:"collapsed"`
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			tmux_target TEXT NOT NULL DEFAULT '',
			cwd TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT 'term',
			group_id INTEGER NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS groups (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			collapsed INTEGER NOT NULL DEFAULT 0
		);
	`)
	return err
}

func (s *Store) CreateSession(sess Session) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO sessions (name, tmux_target, cwd, mode, group_id) VALUES (?, ?, ?, ?, ?)",
		sess.Name, sess.TmuxTarget, sess.Cwd, sess.Mode, sess.GroupID,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.Query("SELECT id, name, tmux_target, cwd, mode, group_id, sort_order FROM sessions ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var v Session
		if err := rows.Scan(&v.ID, &v.Name, &v.TmuxTarget, &v.Cwd, &v.Mode, &v.GroupID, &v.SortOrder); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSession(id int64, u SessionUpdate) error {
	if u.Name != nil {
		s.db.Exec("UPDATE sessions SET name = ? WHERE id = ?", *u.Name, id)
	}
	if u.Mode != nil {
		s.db.Exec("UPDATE sessions SET mode = ? WHERE id = ?", *u.Mode, id)
	}
	if u.GroupID != nil {
		s.db.Exec("UPDATE sessions SET group_id = ? WHERE id = ?", *u.GroupID, id)
	}
	return nil
}

func (s *Store) DeleteSession(id int64) error {
	res, err := s.db.Exec("DELETE FROM sessions WHERE id = ?", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateGroup(name string) (int64, error) {
	res, err := s.db.Exec("INSERT INTO groups (name) VALUES (?)", name)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListGroups() ([]Group, error) {
	rows, err := s.db.Query("SELECT id, name, sort_order, collapsed FROM groups ORDER BY sort_order")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &g.Collapsed); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) UpdateGroup(id int64, name string) error {
	_, err := s.db.Exec("UPDATE groups SET name = ? WHERE id = ?", name, id)
	return err
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go mod tidy && go test ./internal/store/...`
Expected: PASS (4 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/store/ go.mod go.sum
git commit -m "feat: add SQLite store with session/group CRUD and ErrNotFound"
```

---

## Chunk 2: tmux 操作 + HTTP Server + REST API

### Task 4: tmux Executor

**Files:**
- Create: `internal/tmux/executor.go`
- Create: `internal/tmux/executor_test.go`

- [ ] **Step 1: 寫測試（使用 FakeExecutor）**

```go
// internal/tmux/executor_test.go
package tmux_test

import (
	"testing"

	"github.com/wake/tmux-box/internal/tmux"
)

func TestListSessions(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("dev", "/home/user/dev")
	fake.AddSession("prod", "/home/user/prod")

	sessions, err := fake.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("want 2 sessions, got %d", len(sessions))
	}
}

func TestNewSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()

	err := fake.NewSession("test", "/tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !fake.HasSession("test") {
		t.Error("session should exist after creation")
	}
}

func TestKillSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("doomed", "/tmp")

	fake.KillSession("doomed")

	if fake.HasSession("doomed") {
		t.Error("session should not exist after kill")
	}
}

func TestKillNonexistent(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	err := fake.KillSession("nope")
	if err != tmux.ErrNoSession {
		t.Errorf("want ErrNoSession, got %v", err)
	}
}

func TestHasSession(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("exists", "/tmp")

	if !fake.HasSession("exists") {
		t.Error("want true")
	}
	if fake.HasSession("nope") {
		t.Error("want false")
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/tmux/...`
Expected: FAIL

- [ ] **Step 3: 實作**

```go
// internal/tmux/executor.go
package tmux

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var ErrNoSession = errors.New("no such session")

type TmuxSession struct {
	Name string
	Cwd  string
}

// Executor abstracts tmux CLI for testability.
type Executor interface {
	ListSessions() ([]TmuxSession, error)
	NewSession(name, cwd string) error
	KillSession(name string) error
	HasSession(name string) bool
	SendKeys(target, keys string) error
}

// --- Real Executor ---

type RealExecutor struct{}

func NewRealExecutor() *RealExecutor { return &RealExecutor{} }

func (r *RealExecutor) ListSessions() ([]TmuxSession, error) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}\t#{session_path}").Output()
	if err != nil {
		if strings.Contains(err.Error(), "no server running") ||
			strings.Contains(string(out), "no server running") {
			return nil, nil
		}
		// exit status 1 with "no sessions" is normal
		if exitErr, ok := err.(*exec.ExitError); ok {
			if strings.Contains(string(exitErr.Stderr), "no server running") ||
				strings.Contains(string(exitErr.Stderr), "no sessions") {
				return nil, nil
			}
		}
		return nil, fmt.Errorf("tmux list-sessions: %w", err)
	}
	var sessions []TmuxSession
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		s := TmuxSession{Name: parts[0]}
		if len(parts) > 1 {
			s.Cwd = parts[1]
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func (r *RealExecutor) NewSession(name, cwd string) error {
	return exec.Command("tmux", "new-session", "-d", "-s", name, "-c", cwd).Run()
}

func (r *RealExecutor) KillSession(name string) error {
	err := exec.Command("tmux", "kill-session", "-t", name).Run()
	if err != nil {
		return ErrNoSession
	}
	return nil
}

func (r *RealExecutor) HasSession(name string) bool {
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

func (r *RealExecutor) SendKeys(target, keys string) error {
	return exec.Command("tmux", "send-keys", "-t", target, keys, "Enter").Run()
}

// --- Fake Executor ---

type FakeExecutor struct {
	sessions map[string]TmuxSession
}

func NewFakeExecutor() *FakeExecutor {
	return &FakeExecutor{sessions: make(map[string]TmuxSession)}
}

func (f *FakeExecutor) AddSession(name, cwd string) {
	f.sessions[name] = TmuxSession{Name: name, Cwd: cwd}
}

func (f *FakeExecutor) ListSessions() ([]TmuxSession, error) {
	out := make([]TmuxSession, 0, len(f.sessions))
	for _, s := range f.sessions {
		out = append(out, s)
	}
	return out, nil
}

func (f *FakeExecutor) NewSession(name, cwd string) error {
	f.sessions[name] = TmuxSession{Name: name, Cwd: cwd}
	return nil
}

func (f *FakeExecutor) KillSession(name string) error {
	if _, ok := f.sessions[name]; !ok {
		return ErrNoSession
	}
	delete(f.sessions, name)
	return nil
}

func (f *FakeExecutor) HasSession(name string) bool {
	_, ok := f.sessions[name]
	return ok
}

func (f *FakeExecutor) SendKeys(_, _ string) error { return nil }
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/tmux/...`
Expected: PASS (5 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/
git commit -m "feat: add tmux executor with interface and fake for testing"
```

---

### Task 5: HTTP Middleware

**Files:**
- Create: `internal/server/middleware.go`
- Create: `internal/server/middleware_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/server/middleware_test.go
package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wake/tmux-box/internal/server"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })

func TestIPWhitelistAllowed(t *testing.T) {
	h := server.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.50:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestIPWhitelistDenied(t *testing.T) {
	h := server.IPWhitelist([]string{"192.168.1.0/24"})(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Errorf("want 403, got %d", rec.Code)
	}
}

func TestIPWhitelistEmptyAllowsAll(t *testing.T) {
	h := server.IPWhitelist(nil)(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:12345"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthValid(t *testing.T) {
	h := server.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestTokenAuthInvalid(t *testing.T) {
	h := server.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401, got %d", rec.Code)
	}
}

func TestTokenAuthCaseSensitive(t *testing.T) {
	h := server.TokenAuth("Secret")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("want 401 (case mismatch), got %d", rec.Code)
	}
}

func TestTokenAuthQueryParam(t *testing.T) {
	h := server.TokenAuth("secret")(ok)
	req := httptest.NewRequest("GET", "/?token=secret", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200 for valid query param token, got %d", rec.Code)
	}
}

func TestTokenAuthEmptyAllowsAll(t *testing.T) {
	h := server.TokenAuth("")(ok)
	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("want 200, got %d", rec.Code)
	}
}

func TestCORSHeaders(t *testing.T) {
	h := server.CORS(ok)
	req := httptest.NewRequest("OPTIONS", "/", nil)
	req.Header.Set("Origin", "http://example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("want CORS Allow-Origin *")
	}
	if rec.Code != 204 {
		t.Errorf("want 204 for OPTIONS, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/server/...`
Expected: FAIL

- [ ] **Step 3: 實作**

```go
// internal/server/middleware.go
package server

import (
	"net"
	"net/http"
	"strings"
)

// IPWhitelist restricts access by IP. Empty list = allow all.
func IPWhitelist(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	var nets []*net.IPNet
	var ips []net.IP
	for _, a := range allowed {
		if _, cidr, err := net.ParseCIDR(a); err == nil {
			nets = append(nets, cidr)
		} else if ip := net.ParseIP(a); ip != nil {
			ips = append(ips, ip)
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			ip := net.ParseIP(host)
			if ip == nil {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
			for _, cidr := range nets {
				if cidr.Contains(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			for _, a := range ips {
				if a.Equal(ip) {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "forbidden", http.StatusForbidden)
		})
	}
}

// TokenAuth checks Bearer token or ?token= query param. Empty token = allow all.
// Bearer prefix is case-insensitive, token value is case-sensitive.
// Query param fallback enables WebSocket auth (WS API cannot send custom headers).
func TokenAuth(token string) func(http.Handler) http.Handler {
	if token == "" {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check Authorization header first
			if auth := r.Header.Get("Authorization"); len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") && auth[7:] == token {
				next.ServeHTTP(w, r)
				return
			}
			// Fallback: ?token= query param (for WebSocket)
			if r.URL.Query().Get("token") == token {
				next.ServeHTTP(w, r)
				return
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
		})
	}
}

// CORS adds permissive CORS headers. Safe because auth is handled by IP + token.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/server/...`
Expected: PASS (9 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/server/
git commit -m "feat: add IP whitelist, token auth, and CORS middleware"
```

---

### Task 6: Session REST API + HTTP Server

**Files:**
- Create: `internal/server/session_handler.go`
- Create: `internal/server/session_handler_test.go`
- Create: `internal/server/server.go`

- [ ] **Step 1: 寫 handler 測試**

```go
// internal/server/session_handler_test.go
package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func setupHandler(t *testing.T) *server.SessionHandler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return server.NewSessionHandler(db, tmux.NewFakeExecutor())
}

func TestListEmpty(t *testing.T) {
	h := setupHandler(t)
	rec := httptest.NewRecorder()
	h.List(rec, httptest.NewRequest("GET", "/api/sessions", nil))
	if rec.Code != 200 {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	var list []store.Session
	json.NewDecoder(rec.Body).Decode(&list)
	if len(list) != 0 {
		t.Errorf("want empty, got %d", len(list))
	}
}

func TestCreateSession(t *testing.T) {
	h := setupHandler(t)
	body, _ := json.Marshal(map[string]string{"name": "test", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 201 {
		t.Fatalf("want 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var s store.Session
	json.NewDecoder(rec.Body).Decode(&s)
	if s.Name != "test" {
		t.Errorf("want name test, got %s", s.Name)
	}
}

func TestCreateMissingFields(t *testing.T) {
	h := setupHandler(t)
	body, _ := json.Marshal(map[string]string{"name": "test"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))
	if rec.Code != 400 {
		t.Errorf("want 400 for missing cwd, got %d", rec.Code)
	}
}

func TestCreateInvalidJSON(t *testing.T) {
	h := setupHandler(t)
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader([]byte("not json"))))
	if rec.Code != 400 {
		t.Errorf("want 400, got %d", rec.Code)
	}
}

func TestDeleteSession(t *testing.T) {
	h := setupHandler(t)
	// Create
	body, _ := json.Marshal(map[string]string{"name": "del", "cwd": "/tmp", "mode": "term"})
	rec := httptest.NewRecorder()
	h.Create(rec, httptest.NewRequest("POST", "/api/sessions", bytes.NewReader(body)))

	// Delete
	req := httptest.NewRequest("DELETE", "/api/sessions/1", nil)
	req.SetPathValue("id", "1")
	rec = httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != 204 {
		t.Errorf("want 204, got %d", rec.Code)
	}
}

func TestDeleteNotFound(t *testing.T) {
	h := setupHandler(t)
	req := httptest.NewRequest("DELETE", "/api/sessions/999", nil)
	req.SetPathValue("id", "999")
	rec := httptest.NewRecorder()
	h.Delete(rec, req)
	if rec.Code != 404 {
		t.Errorf("want 404, got %d", rec.Code)
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/server/...`
Expected: FAIL — SessionHandler 不存在

- [ ] **Step 3: 實作 session_handler.go**

```go
// internal/server/session_handler.go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

type SessionHandler struct {
	store *store.Store
	tmux  tmux.Executor
}

func NewSessionHandler(s *store.Store, t tmux.Executor) *SessionHandler {
	return &SessionHandler{store: s, tmux: t}
}

type createReq struct {
	Name string `json:"name"`
	Cwd  string `json:"cwd"`
	Mode string `json:"mode"`
}

func (h *SessionHandler) List(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.store.ListSessions()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if sessions == nil {
		sessions = []store.Session{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func (h *SessionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req createReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid json", 400)
		return
	}
	if req.Name == "" || req.Cwd == "" {
		http.Error(w, "name and cwd required", 400)
		return
	}
	if req.Mode == "" {
		req.Mode = "term"
	}

	if err := h.tmux.NewSession(req.Name, req.Cwd); err != nil {
		http.Error(w, "tmux: "+err.Error(), 500)
		return
	}

	sess := store.Session{
		Name:       req.Name,
		TmuxTarget: req.Name + ":0",
		Cwd:        req.Cwd,
		Mode:       req.Mode,
	}
	id, err := h.store.CreateSession(sess)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sess.ID = id
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	json.NewEncoder(w).Encode(sess)
}

func (h *SessionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid id", 400)
		return
	}

	// Find session name for tmux kill
	sessions, _ := h.store.ListSessions()
	for _, s := range sessions {
		if s.ID == id {
			h.tmux.KillSession(s.Name)
			break
		}
	}

	if err := h.store.DeleteSession(id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "not found", 404)
			return
		}
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(204)
}
```

- [ ] **Step 4: 實作 server.go**

```go
// internal/server/server.go
package server

import (
	"fmt"
	"net/http"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

type Server struct {
	cfg   config.Config
	store *store.Store
	tmux  tmux.Executor
	mux   *http.ServeMux
}

func New(cfg config.Config, st *store.Store, tx tmux.Executor) *Server {
	s := &Server{cfg: cfg, store: st, tmux: tx, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) routes() {
	sh := NewSessionHandler(s.store, s.tmux)
	s.mux.HandleFunc("GET /api/sessions", sh.List)
	s.mux.HandleFunc("POST /api/sessions", sh.Create)
	s.mux.HandleFunc("DELETE /api/sessions/{id}", sh.Delete)
}

func (s *Server) Handler() http.Handler {
	var h http.Handler = s.mux
	h = TokenAuth(s.cfg.Token)(h)
	h = IPWhitelist(s.cfg.Allow)(h)
	h = CORS(h)
	return h
}

func (s *Server) ListenAndServe() error {
	addr := fmt.Sprintf("%s:%d", s.cfg.Bind, s.cfg.Port)
	return http.ListenAndServe(addr, s.Handler())
}
```

- [ ] **Step 5: 確認所有測試通過**

Run: `go test ./internal/server/...`
Expected: PASS (15 tests — 9 middleware + 6 handler)

- [ ] **Step 6: Commit**

```bash
git add internal/server/
git commit -m "feat: add session REST API and HTTP server with CORS"
```

---

## Chunk 3: Terminal Relay（WebSocket ↔ PTY + resize）

### Task 7: DataBatcher

**Files:**
- Create: `internal/terminal/batcher.go`
- Create: `internal/terminal/batcher_test.go`

- [ ] **Step 1: 寫測試（使用 channel 同步避免 flaky）**

```go
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
```

- [ ] **Step 2: 確認測試失敗**

Run: `go test ./internal/terminal/...`
Expected: FAIL

- [ ] **Step 3: 實作**

```go
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
	b.onFlush(out)
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
```

- [ ] **Step 4: 確認測試通過**

Run: `go test ./internal/terminal/...`
Expected: PASS (2 tests)

- [ ] **Step 5: Commit**

```bash
git add internal/terminal/
git commit -m "feat: add DataBatcher with time and size thresholds"
```

---

### Task 8: Terminal Relay（含 resize）

**Files:**
- Create: `internal/terminal/relay.go`
- Create: `internal/terminal/relay_test.go`

- [ ] **Step 1: 寫測試**

```go
// internal/terminal/relay_test.go
package terminal_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/wake/tmux-box/internal/terminal"
)

func TestRelayEcho(t *testing.T) {
	// "cat" echoes stdin to stdout via PTY
	relay := terminal.NewRelay("cat", []string{}, "/tmp")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send text
	ws.WriteMessage(websocket.TextMessage, []byte("hello\n"))

	// Read echo (PTY echoes input)
	ws.SetReadDeadline(time.Now().Add(2 * time.Second))
	var received string
	for i := 0; i < 20; i++ {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			break
		}
		received += string(msg)
		if strings.Contains(received, "hello") {
			return // success
		}
	}
	t.Errorf("never received echo, got: %q", received)
}

func TestRelayResize(t *testing.T) {
	relay := terminal.NewRelay("cat", []string{}, "/tmp")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		relay.HandleWebSocket(w, r)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.Close()

	// Send resize message — should not crash
	resize, _ := json.Marshal(terminal.ResizeMsg{Type: "resize", Cols: 120, Rows: 40})
	err = ws.WriteMessage(websocket.TextMessage, resize)
	if err != nil {
		t.Fatal(err)
	}

	// Give it a moment, then verify connection still alive
	time.Sleep(50 * time.Millisecond)
	err = ws.WriteMessage(websocket.TextMessage, []byte("ok\n"))
	if err != nil {
		t.Errorf("connection died after resize: %v", err)
	}
}
```

- [ ] **Step 2: 確認測試失敗**

Run: `go mod tidy && go test ./internal/terminal/...`
Expected: FAIL — Relay 不存在

- [ ] **Step 3: 實作**

```go
// internal/terminal/relay.go
package terminal

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// ResizeMsg is sent from the client to resize the PTY.
type ResizeMsg struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

type Relay struct {
	cmd  string
	args []string
	cwd  string
}

func NewRelay(cmd string, args []string, cwd string) *Relay {
	return &Relay{cmd: cmd, args: args, cwd: cwd}
}

func (r *Relay) HandleWebSocket(w http.ResponseWriter, req *http.Request) {
	conn, err := upgrader.Upgrade(w, req, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	c := exec.Command(r.cmd, r.args...)
	c.Dir = r.cwd
	c.Env = append(os.Environ(), "TERM=xterm-256color")

	ptmx, err := pty.Start(c)
	if err != nil {
		log.Printf("pty start: %v", err)
		return
	}
	defer func() {
		ptmx.Close()
		c.Wait()
	}()

	var wg sync.WaitGroup
	var writeMu sync.Mutex

	// PTY → WebSocket (batched, mutex-protected writes)
	wg.Add(1)
	go func() {
		defer wg.Done()
		batcher := NewBatcher(16*time.Millisecond, 64*1024, func(data []byte) {
			writeMu.Lock()
			conn.WriteMessage(websocket.BinaryMessage, data)
			writeMu.Unlock()
		})
		defer batcher.Stop()
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				batcher.Write(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("pty read: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket → PTY (with resize handling)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			// Check if it's a resize message
			var resize ResizeMsg
			if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
				pty.Setsize(ptmx, &pty.Winsize{Cols: resize.Cols, Rows: resize.Rows})
				continue
			}
			// Regular input
			ptmx.Write(msg)
		}
	}()

	wg.Wait()
}
```

- [ ] **Step 4: 確認測試通過**

Run: `go mod tidy && go test ./internal/terminal/... -v`
Expected: PASS (4 tests — 2 batcher + 2 relay)

- [ ] **Step 5: Commit**

```bash
git add internal/terminal/ go.mod go.sum
git commit -m "feat: add terminal relay with WebSocket, PTY, and resize support"
```

---

### Task 9: 整合 Terminal WebSocket 到 Server + main.go

**Files:**
- Modify: `internal/server/server.go`
- Modify: `cmd/tbox/main.go`

- [ ] **Step 1: 在 server.go 加入 WebSocket 路由**

在 `server.go` import 加入 `"github.com/wake/tmux-box/internal/terminal"`。

在 `routes()` 加入：

```go
s.mux.HandleFunc("/ws/terminal/{session}", s.handleTerminal)
```

在 `Server` 上加入方法：

```go
func (s *Server) handleTerminal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("session")
	if !s.tmux.HasSession(name) {
		http.Error(w, "session not found", 404)
		return
	}
	relay := terminal.NewRelay("tmux", []string{"attach-session", "-t", name}, "/tmp")
	relay.HandleWebSocket(w, r)
}
```

- [ ] **Step 2: 更新 main.go**

```go
// cmd/tbox/main.go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wake/tmux-box/internal/config"
	"github.com/wake/tmux-box/internal/server"
	"github.com/wake/tmux-box/internal/store"
	"github.com/wake/tmux-box/internal/tmux"
)

func main() {
	configPath := flag.String("config", "", "path to config.toml (default: ~/.config/tbox/config.toml)")
	bind := flag.String("bind", "", "override bind address")
	port := flag.Int("port", 0, "override port")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *bind != "" {
		cfg.Bind = *bind
	}
	if *port != 0 {
		cfg.Port = *port
	}

	os.MkdirAll(cfg.DataDir, 0755)

	st, err := store.Open(filepath.Join(cfg.DataDir, "state.db"))
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer st.Close()

	tx := tmux.NewRealExecutor()
	srv := server.New(cfg, st, tx)

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		fmt.Println("\nshutting down...")
		os.Exit(0)
	}()

	addr := fmt.Sprintf("%s:%d", cfg.Bind, cfg.Port)
	log.Printf("tbox daemon listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 3: 驗證 build + 手動測試**

Run:
```bash
make build
./bin/tbox --port 17860 &
tmux new-session -d -s test-relay
curl -s http://localhost:17860/api/sessions | head
kill %1
tmux kill-session -t test-relay
```
Expected: daemon 啟動成功，API 回傳 JSON

- [ ] **Step 4: Commit**

```bash
git add internal/server/server.go cmd/tbox/main.go
git commit -m "feat: wire terminal WebSocket and daemon main entry"
```

---

## Chunk 4: React SPA + xterm.js

### Task 10: SPA 專案初始化

**Files:**
- Create: `spa/` (Vite scaffold)

- [ ] **Step 1: Scaffold + 清理 + 安裝依賴**

```bash
cd /Users/wake/Workspace/wake/tmux-box
pnpm create vite spa --template react-ts
cd spa
rm -f src/App.css src/assets/react.svg public/vite.svg
pnpm install
pnpm add -D tailwindcss @tailwindcss/vite vitest @testing-library/react @testing-library/jest-dom jsdom
pnpm add zustand @xterm/xterm @xterm/addon-fit @xterm/addon-webgl
```

- [ ] **Step 2: 設定 Vite + Tailwind + Vitest**

```typescript
// spa/vite.config.ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/api': 'http://localhost:7860',
      '/ws': { target: 'ws://localhost:7860', ws: true },
    },
  },
  test: {
    environment: 'jsdom',
    setupFiles: './src/test-setup.ts',
  },
})
```

```css
/* spa/src/index.css */
@import "tailwindcss";
```

```typescript
// spa/src/test-setup.ts
import '@testing-library/jest-dom/vitest'
```

更新 `spa/src/main.tsx` — 確認只 import `./index.css` 和 `./App`（刪除 `App.css` 的 import）。

- [ ] **Step 3: 建立基本 App.tsx**

```tsx
// spa/src/App.tsx
export default function App() {
  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <div className="w-56 bg-gray-900 border-r border-gray-800 p-3">
        <h2 className="text-xs uppercase text-gray-500 mb-2">Sessions</h2>
      </div>
      <div className="flex-1 flex items-center justify-center">
        <p className="text-gray-500">Select a session</p>
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 驗證 dev server + test runner**

Run: `cd spa && pnpm dev` (驗證頁面可載入)
Run: `cd spa && pnpm vitest run` (應 0 test，0 fail)

- [ ] **Step 5: Commit**

```bash
cd /Users/wake/Workspace/wake/tmux-box
git add spa/
git commit -m "feat: init React SPA with Vite, Tailwind, and Vitest"
```

---

### Task 11: API Client + Session Store (TDD)

**Files:**
- Create: `spa/src/lib/api.ts`
- Create: `spa/src/lib/api.test.ts`
- Create: `spa/src/stores/useSessionStore.ts`
- Create: `spa/src/stores/useSessionStore.test.ts`

- [ ] **Step 1: 寫 API client 測試**

```typescript
// spa/src/lib/api.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { listSessions, createSession, deleteSession, type Session } from './api'

const mockSession: Session = {
  id: 1, name: 'test', tmux_target: 'test:0',
  cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0,
}

beforeEach(() => {
  vi.restoreAllMocks()
})

describe('listSessions', () => {
  it('returns sessions from API', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify([mockSession]), { status: 200 })
    )
    const sessions = await listSessions('http://localhost:7860')
    expect(sessions).toEqual([mockSession])
  })

  it('throws on error status', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response('error', { status: 500, statusText: 'Internal Server Error' })
    )
    await expect(listSessions('http://localhost:7860')).rejects.toThrow('500')
  })
})

describe('createSession', () => {
  it('posts and returns created session', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(mockSession), { status: 201 })
    )
    const s = await createSession('http://localhost:7860', 'test', '/tmp', 'term')
    expect(s.name).toBe('test')
  })
})

describe('deleteSession', () => {
  it('sends DELETE request', async () => {
    const spy = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(null, { status: 204 })
    )
    await deleteSession('http://localhost:7860', 1)
    expect(spy).toHaveBeenCalledWith(
      'http://localhost:7860/api/sessions/1',
      expect.objectContaining({ method: 'DELETE' })
    )
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/lib/api.test.ts`
Expected: FAIL — module 不存在

- [ ] **Step 3: 實作 API client**

```typescript
// spa/src/lib/api.ts
export interface Session {
  id: number
  name: string
  tmux_target: string
  cwd: string
  mode: string
  group_id: number
  sort_order: number
}

export async function listSessions(base: string): Promise<Session[]> {
  const res = await fetch(`${base}/api/sessions`)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function createSession(
  base: string, name: string, cwd: string, mode: string,
): Promise<Session> {
  const res = await fetch(`${base}/api/sessions`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, cwd, mode }),
  })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json()
}

export async function deleteSession(base: string, id: number): Promise<void> {
  const res = await fetch(`${base}/api/sessions/${id}`, { method: 'DELETE' })
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}
```

- [ ] **Step 4: 確認 API 測試通過**

Run: `cd spa && pnpm vitest run src/lib/api.test.ts`
Expected: PASS (4 tests)

- [ ] **Step 5: 寫 Session Store 測試**

```typescript
// spa/src/stores/useSessionStore.test.ts
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { act, renderHook } from '@testing-library/react'
import { useSessionStore } from './useSessionStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([
    { id: 1, name: 'test', tmux_target: 'test:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
  ]),
}))

beforeEach(() => {
  // Reset zustand store between tests
  useSessionStore.setState({ sessions: [], activeId: null })
})

describe('useSessionStore', () => {
  it('fetches sessions', async () => {
    const { result } = renderHook(() => useSessionStore())
    await act(async () => { await result.current.fetch('http://localhost:7860') })
    expect(result.current.sessions).toHaveLength(1)
    expect(result.current.sessions[0].name).toBe('test')
  })

  it('sets active session', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive(1) })
    expect(result.current.activeId).toBe(1)
  })

  it('persists to localStorage', () => {
    const { result } = renderHook(() => useSessionStore())
    act(() => { result.current.setActive(42) })
    // Zustand persist middleware writes to localStorage
    const stored = JSON.parse(localStorage.getItem('tbox-sessions') || '{}')
    expect(stored.state?.activeId).toBe(42)
  })
})
```

- [ ] **Step 6: 實作 Session Store**

```typescript
// spa/src/stores/useSessionStore.ts
import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import { type Session, listSessions } from '../lib/api'

interface SessionState {
  sessions: Session[]
  activeId: number | null
  fetch: (base: string) => Promise<void>
  setActive: (id: number | null) => void
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set) => ({
      sessions: [],
      activeId: null,
      fetch: async (base: string) => {
        const sessions = await listSessions(base)
        set({ sessions })
      },
      setActive: (id) => set({ activeId: id }),
    }),
    { name: 'tbox-sessions' },
  ),
)
```

- [ ] **Step 7: 確認所有前端測試通過**

Run: `cd spa && pnpm vitest run`
Expected: PASS (7 tests)

- [ ] **Step 8: Commit**

```bash
git add spa/src/lib/ spa/src/stores/
git commit -m "feat: add API client and session store with TDD and localStorage persistence"
```

---

### Task 12: SessionPanel 元件 (TDD)

**Files:**
- Create: `spa/src/components/SessionPanel.tsx`
- Create: `spa/src/components/SessionPanel.test.tsx`
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 寫測試**

```tsx
// spa/src/components/SessionPanel.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import SessionPanel from './SessionPanel'
import { useSessionStore } from '../stores/useSessionStore'

vi.mock('../lib/api', () => ({
  listSessions: vi.fn().mockResolvedValue([]),
}))

describe('SessionPanel', () => {
  it('shows empty state', () => {
    useSessionStore.setState({ sessions: [], activeId: null })
    render(<SessionPanel />)
    expect(screen.getByText('No sessions')).toBeInTheDocument()
  })

  it('renders session list', () => {
    useSessionStore.setState({
      sessions: [
        { id: 1, name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
        { id: 2, name: 'prod', tmux_target: 'prod:0', cwd: '/tmp', mode: 'stream', group_id: 0, sort_order: 0 },
      ],
      activeId: null,
    })
    render(<SessionPanel />)
    expect(screen.getByText('dev')).toBeInTheDocument()
    expect(screen.getByText('prod')).toBeInTheDocument()
  })

  it('highlights active session', () => {
    useSessionStore.setState({
      sessions: [
        { id: 1, name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
      ],
      activeId: 1,
    })
    render(<SessionPanel />)
    const btn = screen.getByRole('button', { name: /dev/i })
    expect(btn.className).toContain('bg-gray-800')
  })

  it('sets active on click', () => {
    const setActive = vi.fn()
    useSessionStore.setState({
      sessions: [
        { id: 1, name: 'dev', tmux_target: 'dev:0', cwd: '/tmp', mode: 'term', group_id: 0, sort_order: 0 },
      ],
      activeId: null,
      setActive,
    })
    render(<SessionPanel />)
    fireEvent.click(screen.getByRole('button', { name: /dev/i }))
    expect(setActive).toHaveBeenCalledWith(1)
  })
})
```

- [ ] **Step 2: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/components/SessionPanel.test.tsx`
Expected: FAIL

- [ ] **Step 3: 實作**

```tsx
// spa/src/components/SessionPanel.tsx
import { useSessionStore } from '../stores/useSessionStore'

const modeIcon: Record<string, string> = { term: '❯', stream: '●', jsonl: '◐' }

export default function SessionPanel() {
  const { sessions, activeId, setActive } = useSessionStore()

  return (
    <div className="w-56 bg-gray-900 border-r border-gray-800 p-3 flex flex-col">
      <h2 className="text-xs uppercase text-gray-500 mb-3">Sessions</h2>
      <div className="flex-1 overflow-y-auto space-y-1">
        {sessions.length === 0 && <p className="text-sm text-gray-600">No sessions</p>}
        {sessions.map((s) => (
          <button
            key={s.id}
            onClick={() => setActive(s.id)}
            className={`w-full text-left px-2 py-1.5 rounded text-sm ${
              activeId === s.id ? 'bg-gray-800 text-gray-100' : 'text-gray-400 hover:bg-gray-800/50'
            }`}
          >
            <span className="mr-1.5">{modeIcon[s.mode] ?? '❯'}</span>
            {s.name}
            <span className="float-right text-xs text-gray-600">{s.mode}</span>
          </button>
        ))}
      </div>
    </div>
  )
}
```

- [ ] **Step 4: 確認測試通過**

Run: `cd spa && pnpm vitest run src/components/SessionPanel.test.tsx`
Expected: PASS (4 tests)

- [ ] **Step 5: 更新 App.tsx**

```tsx
// spa/src/App.tsx
import SessionPanel from './components/SessionPanel'

export default function App() {
  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1 flex items-center justify-center">
        <p className="text-gray-500">Select a session</p>
      </div>
    </div>
  )
}
```

- [ ] **Step 6: Commit**

```bash
git add spa/src/components/SessionPanel.tsx spa/src/components/SessionPanel.test.tsx spa/src/App.tsx
git commit -m "feat: add SessionPanel component with TDD"
```

---

### Task 13: TerminalView + WebSocket（TDD）

**Files:**
- Create: `spa/src/lib/ws.ts`
- Create: `spa/src/components/TerminalView.tsx`
- Create: `spa/src/components/TerminalView.test.tsx`
- Modify: `spa/src/App.tsx`

- [ ] **Step 1: 建立 WebSocket 管理**

```typescript
// spa/src/lib/ws.ts
export interface TerminalConnection {
  send: (data: string) => void
  resize: (cols: number, rows: number) => void
  close: () => void
}

export function connectTerminal(
  url: string,
  onData: (data: ArrayBuffer) => void,
  onClose: () => void,
): TerminalConnection {
  const ws = new WebSocket(url)
  ws.binaryType = 'arraybuffer'

  ws.onmessage = (e) => onData(e.data)
  ws.onclose = () => onClose()
  ws.onerror = () => onClose()

  return {
    send: (data) => {
      if (ws.readyState === WebSocket.OPEN) ws.send(data)
    },
    resize: (cols, rows) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols, rows }))
      }
    },
    close: () => ws.close(),
  }
}
```

- [ ] **Step 2: 寫 TerminalView 測試**

```tsx
// spa/src/components/TerminalView.test.tsx
import { describe, it, expect, vi } from 'vitest'
import { render } from '@testing-library/react'
import TerminalView from './TerminalView'

// xterm.js requires DOM APIs not available in jsdom, so we test mounting only
vi.mock('@xterm/xterm', () => ({
  Terminal: vi.fn().mockImplementation(() => ({
    loadAddon: vi.fn(),
    open: vi.fn(),
    write: vi.fn(),
    onData: vi.fn(),
    onResize: vi.fn(),
    dispose: vi.fn(),
  })),
}))

vi.mock('@xterm/addon-fit', () => ({
  FitAddon: vi.fn().mockImplementation(() => ({
    fit: vi.fn(),
    dispose: vi.fn(),
  })),
}))

vi.mock('@xterm/addon-webgl', () => ({
  WebglAddon: vi.fn().mockImplementation(() => ({
    dispose: vi.fn(),
  })),
}))

vi.mock('../lib/ws', () => ({
  connectTerminal: vi.fn().mockReturnValue({
    send: vi.fn(),
    resize: vi.fn(),
    close: vi.fn(),
  }),
}))

describe('TerminalView', () => {
  it('renders container div', () => {
    const { container } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    expect(container.querySelector('div')).toBeInTheDocument()
  })

  it('cleans up on unmount', () => {
    const { unmount } = render(
      <TerminalView wsUrl="ws://localhost:7860/ws/terminal/test" />
    )
    // Should not throw
    unmount()
  })
})
```

- [ ] **Step 3: 確認測試失敗**

Run: `cd spa && pnpm vitest run src/components/TerminalView.test.tsx`
Expected: FAIL

- [ ] **Step 4: 實作**

```tsx
// spa/src/components/TerminalView.tsx
import { useEffect, useRef } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import { connectTerminal } from '../lib/ws'
import '@xterm/xterm/css/xterm.css'

interface Props {
  wsUrl: string
}

export default function TerminalView({ wsUrl }: Props) {
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!containerRef.current) return

    const term = new Terminal({
      theme: { background: '#0a0a1a', foreground: '#e0e0e0' },
      fontSize: 14,
      fontFamily: 'Menlo, Monaco, monospace',
      cursorBlink: true,
    })

    const fitAddon = new FitAddon()
    term.loadAddon(fitAddon)
    term.open(containerRef.current)

    try { term.loadAddon(new WebglAddon()) } catch { /* fallback to canvas */ }

    fitAddon.fit()

    const conn = connectTerminal(
      wsUrl,
      (data) => term.write(new Uint8Array(data)),
      () => term.write('\r\n\x1b[31m[disconnected]\x1b[0m\r\n'),
    )

    term.onData((data) => conn.send(data))
    term.onResize(({ cols, rows }) => conn.resize(cols, rows))

    const observer = new ResizeObserver(() => fitAddon.fit())
    observer.observe(containerRef.current)

    return () => {
      observer.disconnect()
      conn.close()
      term.dispose()
    }
  }, [wsUrl])

  return <div ref={containerRef} className="w-full h-full" />
}
```

- [ ] **Step 5: 確認測試通過**

Run: `cd spa && pnpm vitest run src/components/TerminalView.test.tsx`
Expected: PASS (2 tests)

- [ ] **Step 6: 更新 App.tsx 串接**

```tsx
// spa/src/App.tsx
import SessionPanel from './components/SessionPanel'
import TerminalView from './components/TerminalView'
import { useSessionStore } from './stores/useSessionStore'

export default function App() {
  const { sessions, activeId } = useSessionStore()
  const active = sessions.find((s) => s.id === activeId)

  // TODO: make daemon base URL configurable via host management
  const daemonBase = 'localhost:7860'
  const wsProtocol = location.protocol === 'https:' ? 'wss:' : 'ws:'

  return (
    <div className="h-screen bg-gray-950 text-gray-200 flex">
      <SessionPanel />
      <div className="flex-1">
        {active ? (
          <TerminalView wsUrl={`${wsProtocol}//${daemonBase}/ws/terminal/${active.name}`} />
        ) : (
          <div className="flex items-center justify-center h-full">
            <p className="text-gray-500">Select a session</p>
          </div>
        )}
      </div>
    </div>
  )
}
```

- [ ] **Step 7: 確認所有前端測試通過**

Run: `cd spa && pnpm vitest run`
Expected: PASS (all tests)

- [ ] **Step 8: Commit**

```bash
git add spa/src/
git commit -m "feat: add TerminalView with xterm.js, WebSocket, and resize"
```

---

## 驗收標準

Phase 1 完成後：

1. `make build` → `bin/tbox`
2. `tbox --bind 100.64.0.2 --port 7860` 啟動 daemon
3. `cd spa && pnpm dev` 啟動 SPA dev server
4. 瀏覽器開啟 SPA → 連到 daemon
5. API 建立 session → tmux session 被建立 → 左側面板出現
6. 點擊 session → xterm.js 終端出現 → 可正常操作
7. 調整瀏覽器視窗 → 終端自動 resize
8. `make test` Go 測試全過
9. `cd spa && pnpm vitest run` 前端測試全過
10. 關閉 SPA 重開 → active session 和 UI 狀態仍在（localStorage）
