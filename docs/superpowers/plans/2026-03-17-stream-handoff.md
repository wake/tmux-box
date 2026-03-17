# Stream Handoff 雙向切換 Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 實現 term（互動式 CC）與 stream（`-p` 串流模式）之間的雙向 handoff，使用 `--resume <session_id>` 精確續接同一 CC 對話。

**Architecture:** 後端 `handoff_handler.go` 新增 session ID 擷取（注入 `/status` → capture pane → regex 解析）與反向 handoff（`runHandoffToTerm`）。前端改為雙 View 同時掛載（CSS display 切換），新增 "Handoff to Term" 按鈕。

**Tech Stack:** Go 1.26 / React 19 / Zustand / xterm.js / Vitest / tmux CLI

**Spec:** `docs/superpowers/specs/2026-03-17-stream-handoff-design.md`

---

## File Map

### Go — 新增/修改

| 檔案 | 變更 |
|------|------|
| `internal/tmux/executor.go` | 新增 `SendKeysRaw` interface + Real/Fake 實作 |
| `internal/tmux/executor_test.go` | 新增 `SendKeysRaw` 測試 |
| `internal/detect/extract.go` | **新建** — `ExtractSessionID()` 函式 |
| `internal/detect/extract_test.go` | **新建** — 解析測試 |
| `internal/store/store.go` | `cc_session_id` 欄位：migrate、Session struct、CRUD |
| `internal/store/store_test.go` | `cc_session_id` CRUD 測試 |
| `internal/server/handoff_handler.go` | mode 驗證重構 + `runHandoff` 改寫 + `runHandoffToTerm` 新增 |
| `internal/server/handoff_handler_test.go` | 新增 mode=term 測試、調整既有測試 |

### Frontend — 修改

| 檔案 | 變更 |
|------|------|
| `spa/src/lib/api.ts` | `handoff()` preset 改 optional、`Session` 加 `cc_session_id` |
| `spa/src/components/HandoffButton.tsx` | 按鈕文字 → "Handoff"、啟用條件、新進度標籤 |
| `spa/src/components/StreamInput.tsx` | 新增 "Handoff to Term" 按鈕 |
| `spa/src/components/ConversationView.tsx` | `onHandoffToTerm` prop、`clear()` 保護 |
| `spa/src/App.tsx` | 雙 View 掛載、`handleHandoffToTerm` handler |

---

## Task 0: 驗證 CC `/status` 輸出格式

**前提**：此任務必須在有 Claude Code 安裝的環境中手動執行。結果決定 Task 2 的 regex。

- [ ] **Step 1: 在 tmux session 中啟動 CC**

```bash
tmux new-session -d -s handoff-test
tmux send-keys -t handoff-test "claude" Enter
# 等 CC 啟動完成（出現 ❯ prompt）
```

- [ ] **Step 2: 注入 /status 並擷取輸出**

```bash
tmux send-keys -t handoff-test "/status" Enter
sleep 2
tmux capture-pane -t handoff-test -p -S -40
```

- [ ] **Step 3: 記錄 session ID 格式**

在輸出中找到 `Session:` 行，記錄 session ID 的格式（UUID? 其他?）。
在 spec 文件中更新 regex（如有必要）。

- [ ] **Step 4: 驗證 Escape 行為**

```bash
tmux send-keys -t handoff-test Escape
sleep 0.5
tmux capture-pane -t handoff-test -p -S -5
# 確認是否回到 ❯ prompt
```

- [ ] **Step 5: 清理**

```bash
tmux send-keys -t handoff-test "/exit" Enter
tmux kill-session -t handoff-test
```

---

## Task 1: tmux Executor — SendKeysRaw

**Files:**
- Modify: `internal/tmux/executor.go:19-27` (interface) + `:82-84` (Real) + `:151` (Fake)
- Test: `internal/tmux/executor_test.go`

- [ ] **Step 1: 寫失敗測試**

在 `internal/tmux/executor_test.go` 新增：

```go
func TestSendKeysRaw(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u")
	if err != nil {
		t.Fatal(err)
	}

	// Verify raw keys are recorded (no Enter appended)
	keys := fake.RawKeysSent()
	if len(keys) != 1 || keys[0].Target != "test" || keys[0].Keys[0] != "C-u" {
		t.Errorf("unexpected raw keys: %+v", keys)
	}
}

func TestSendKeysRawMultiple(t *testing.T) {
	fake := tmux.NewFakeExecutor()
	fake.AddSession("test", "/tmp")

	err := fake.SendKeysRaw("test", "C-u", "C-c")
	if err != nil {
		t.Fatal(err)
	}

	keys := fake.RawKeysSent()
	if len(keys) != 1 || len(keys[0].Keys) != 2 {
		t.Errorf("want 1 call with 2 keys, got %+v", keys)
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/tmux/ -v -run TestSendKeysRaw`
Expected: compilation error — `SendKeysRaw` not defined

- [ ] **Step 3: 實作 SendKeysRaw**

修改 `internal/tmux/executor.go`：

1. Interface 新增：

```go
// Executor interface — 在 SendKeys 之後新增
SendKeysRaw(target string, keys ...string) error
```

2. RealExecutor 實作：

```go
func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
	args := []string{"send-keys", "-t", target}
	args = append(args, keys...)
	return exec.Command("tmux", args...).Run()
}
```

3. FakeExecutor 新增記錄能力：

```go
// 在 FakeExecutor struct 內新增欄位
type RawKeysCall struct {
	Target string
	Keys   []string
}

// FakeExecutor struct 新增
rawKeysCalls []RawKeysCall

func (f *FakeExecutor) SendKeysRaw(target string, keys ...string) error {
	f.rawKeysCalls = append(f.rawKeysCalls, RawKeysCall{Target: target, Keys: keys})
	return nil
}

func (f *FakeExecutor) RawKeysSent() []RawKeysCall {
	return f.rawKeysCalls
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/tmux/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tmux/executor.go internal/tmux/executor_test.go
git commit -m "feat: add SendKeysRaw to tmux Executor for control key injection"
```

---

## Task 2: detect — ExtractSessionID

**Files:**
- Create: `internal/detect/extract.go`
- Create: `internal/detect/extract_test.go`

- [ ] **Step 1: 寫失敗測試**

建立 `internal/detect/extract_test.go`：

```go
package detect

import (
	"testing"
)

func TestExtractSessionID(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantID  string
		wantErr bool
	}{
		{
			name: "standard /status output",
			content: `╭─ Status ──────────────────────────────────╮
│ Session: 01abc234-5678-9def-0123-456789abcdef │
│ Model: claude-sonnet-4-20250514             │
│ Context: 45,231 / 200,000 tokens          │
╰───────────────────────────────────────────╯
❯ `,
			wantID: "01abc234-5678-9def-0123-456789abcdef",
		},
		{
			name:    "no session ID in content",
			content: "❯ \nsome random text\n",
			wantErr: true,
		},
		{
			name:    "empty content",
			content: "",
			wantErr: true,
		},
		{
			name: "session ID buried in noise",
			content: `lots of output here
Session: deadbeef-1234-5678-9abc-def012345678
more output`,
			wantID: "deadbeef-1234-5678-9abc-def012345678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ExtractSessionID(tt.content)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id != tt.wantID {
				t.Errorf("want %q, got %q", tt.wantID, id)
			}
		})
	}
}
```

**注意**：測試中的 UUID 格式基於 spec 推測。Task 0 完成後，根據實際 `/status` 輸出調整測試資料和 regex。

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/detect/ -v -run TestExtractSessionID`
Expected: compilation error — `ExtractSessionID` not defined

- [ ] **Step 3: 實作 ExtractSessionID**

建立 `internal/detect/extract.go`：

```go
package detect

import (
	"errors"
	"regexp"
)

var errNoSessionID = errors.New("session ID not found in pane content")

// sessionIDRegex matches "Session: <uuid>" in CC /status output.
// Adjust this regex after verifying actual /status output format (Task 0).
var sessionIDRegex = regexp.MustCompile(
	`Session:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`,
)

// ExtractSessionID parses CC /status output to find the session UUID.
func ExtractSessionID(paneContent string) (string, error) {
	m := sessionIDRegex.FindStringSubmatch(paneContent)
	if len(m) < 2 {
		return "", errNoSessionID
	}
	return m[1], nil
}
```

- [ ] **Step 4: 執行測試確認通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/detect/ -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add internal/detect/extract.go internal/detect/extract_test.go
git commit -m "feat: add ExtractSessionID for parsing CC /status output"
```

---

## Task 3: store — cc_session_id 欄位

**Files:**
- Modify: `internal/store/store.go:19-28` (Session struct), `:37-41` (SessionUpdate), `:64-125` (migrate), `:141-155` (ListSessions), `:158-193` (UpdateSession), `:208-219` (GetSession)
- Test: `internal/store/store_test.go`

- [ ] **Step 1: 寫失敗測試**

在 `internal/store/store_test.go` 新增：

```go
func TestCCSessionID(t *testing.T) {
	db := openTestDB(t)

	// Create session — cc_session_id 預設為空
	id, err := db.CreateSession(store.Session{
		Name: "test", TmuxTarget: "test:0", Cwd: "/tmp", Mode: "term",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Get — 確認預設為空
	sess, err := db.GetSession(id)
	if err != nil {
		t.Fatal(err)
	}
	if sess.CCSessionID != "" {
		t.Errorf("want empty cc_session_id, got %q", sess.CCSessionID)
	}

	// Update — 寫入 session ID
	ccID := "01abc234-5678-9def-0123-456789abcdef"
	err = db.UpdateSession(id, store.SessionUpdate{CCSessionID: ptr(ccID)})
	if err != nil {
		t.Fatal(err)
	}

	sess, _ = db.GetSession(id)
	if sess.CCSessionID != ccID {
		t.Errorf("want %q, got %q", ccID, sess.CCSessionID)
	}

	// List — 確認也包含 cc_session_id
	sessions, _ := db.ListSessions()
	if sessions[0].CCSessionID != ccID {
		t.Errorf("list: want %q, got %q", ccID, sessions[0].CCSessionID)
	}

	// Clear
	err = db.UpdateSession(id, store.SessionUpdate{CCSessionID: ptr("")})
	if err != nil {
		t.Fatal(err)
	}
	sess, _ = db.GetSession(id)
	if sess.CCSessionID != "" {
		t.Errorf("want empty after clear, got %q", sess.CCSessionID)
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/store/ -v -run TestCCSessionID`
Expected: compilation error — `CCSessionID` not a field

- [ ] **Step 3: 修改 Session struct 和 SessionUpdate**

在 `internal/store/store.go`：

Session struct 新增欄位：
```go
type Session struct {
	ID          int64  `json:"id"`
	UID         string `json:"uid"`
	Name        string `json:"name"`
	TmuxTarget  string `json:"tmux_target"`
	Cwd         string `json:"cwd"`
	Mode        string `json:"mode"`
	GroupID     int64  `json:"group_id"`
	SortOrder   int    `json:"sort_order"`
	CCSessionID string `json:"cc_session_id"`
}
```

SessionUpdate struct 新增欄位：
```go
type SessionUpdate struct {
	Name        *string `json:"name,omitempty"`
	Mode        *string `json:"mode,omitempty"`
	GroupID     *int64  `json:"group_id,omitempty"`
	CCSessionID *string `json:"cc_session_id,omitempty"`
}
```

- [ ] **Step 4: 新增 migration**

在 `migrate()` 函式中，`// Ensure UID uniqueness` 行之前新增：

```go
// Migration: add cc_session_id column if missing
var hasCCSessionID bool
rows3, _ := db.Query("PRAGMA table_info(sessions)")
if rows3 != nil {
	defer rows3.Close()
	for rows3.Next() {
		var cid int
		var name, typ string
		var notnull int
		var dflt sql.NullString
		var pk int
		rows3.Scan(&cid, &name, &typ, &notnull, &dflt, &pk)
		if name == "cc_session_id" {
			hasCCSessionID = true
		}
	}
}
if !hasCCSessionID {
	if _, err := db.Exec("ALTER TABLE sessions ADD COLUMN cc_session_id TEXT NOT NULL DEFAULT ''"); err != nil {
		return fmt.Errorf("add cc_session_id column: %w", err)
	}
}
```

- [ ] **Step 5: 更新 SQL queries**

`ListSessions` — SELECT 加入 `cc_session_id`，Scan 加入 `&v.CCSessionID`：
```go
rows, err := s.db.Query("SELECT id, uid, name, tmux_target, cwd, mode, group_id, sort_order, cc_session_id FROM sessions ORDER BY sort_order")
// ...
rows.Scan(&v.ID, &v.UID, &v.Name, &v.TmuxTarget, &v.Cwd, &v.Mode, &v.GroupID, &v.SortOrder, &v.CCSessionID)
```

`GetSession` — 同上：
```go
s.db.QueryRow(
	"SELECT id, uid, name, tmux_target, cwd, mode, group_id, sort_order, cc_session_id FROM sessions WHERE id = ?", id,
).Scan(&sess.ID, &sess.UID, &sess.Name, &sess.TmuxTarget, &sess.Cwd, &sess.Mode, &sess.GroupID, &sess.SortOrder, &sess.CCSessionID)
```

`UpdateSession` — 新增 CCSessionID 分支（在 GroupID 分支之後）：
```go
if u.CCSessionID != nil {
	res, err := s.db.Exec("UPDATE sessions SET cc_session_id = ? WHERE id = ?", *u.CCSessionID, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n > 0 {
		updated = true
	}
}
```

- [ ] **Step 6: 執行測試確認通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/store/ -v`
Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add cc_session_id column to sessions table"
```

---

## Task 4: handoff_handler — mode 驗證重構

**Files:**
- Modify: `internal/server/handoff_handler.go:58-129`
- Test: `internal/server/handoff_handler_test.go`

- [ ] **Step 1: 寫新增的測試**

在 `internal/server/handoff_handler_test.go` 新增：

```go
func TestHandoffTermMode(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	id, err := db.CreateSession(store.Session{
		Name: "test-session", TmuxTarget: "test-session:0", Cwd: "/tmp", Mode: "stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Set cc_session_id so handoff-to-term has something to work with
	ccID := "01abc234-5678-9def-0123-456789abcdef"
	db.UpdateSession(id, store.SessionUpdate{CCSessionID: &ccID})

	// POST handoff with mode=term (no preset)
	body, _ := json.Marshal(map[string]string{"mode": "term"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}

	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	if result["handoff_id"] == "" {
		t.Fatal("expected handoff_id in response")
	}

	time.Sleep(100 * time.Millisecond)
}

func TestHandoffTermModeNoPresetRequired(t *testing.T) {
	srv, db := newHandoffTestServer(t)

	_, err := db.CreateSession(store.Session{
		Name: "test-session", TmuxTarget: "test-session:0", Cwd: "/tmp", Mode: "stream",
	})
	if err != nil {
		t.Fatal(err)
	}

	// mode=term with no preset should be accepted (not 400)
	body, _ := json.Marshal(map[string]string{"mode": "term"})
	resp, err := http.Post(srv.URL+"/api/sessions/1/handoff", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 執行測試確認失敗**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/server/ -v -run TestHandoffTermMode`
Expected: FAIL — mode "term" rejected as 400

- [ ] **Step 3: 重構 handleHandoff 驗證邏輯**

修改 `internal/server/handoff_handler.go` 的 `handleHandoff`：

```go
// 替換原本的 mode 驗證
if req.Mode != "stream" && req.Mode != "jsonl" && req.Mode != "term" {
	http.Error(w, "mode must be stream, jsonl, or term", http.StatusBadRequest)
	return
}

// Preset lookup only for stream/jsonl
var command string
if req.Mode != "term" {
	for _, p := range presets {
		if p.Name == req.Preset {
			command = p.Command
			break
		}
	}
	if command == "" {
		http.Error(w, "preset not found", http.StatusBadRequest)
		return
	}
}
```

修改 goroutine 啟動分流：
```go
// 替換原本的 go s.runHandoff(...)
if req.Mode == "term" {
	go s.runHandoffToTerm(sess, handoffID)
} else {
	go s.runHandoff(sess, req.Mode, command, handoffID, token, port, bind)
}
```

新增空的 `runHandoffToTerm` stub（讓編譯通過）：
```go
func (s *Server) runHandoffToTerm(sess store.Session, handoffID string) {
	defer s.handoffLocks.Unlock(sess.Name)
	s.events.Broadcast(sess.Name, "handoff", "failed:not implemented")
}
```

- [ ] **Step 4: 執行全部 handoff 測試確認通過**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/server/ -v -run TestHandoff`
Expected: all PASS（包含既有測試和新測試）

- [ ] **Step 5: Commit**

```bash
git add internal/server/handoff_handler.go internal/server/handoff_handler_test.go
git commit -m "feat: accept mode=term in handoff endpoint, add runHandoffToTerm stub"
```

---

## Task 5: handoff_handler — runHandoff 改寫

**Files:**
- Modify: `internal/server/handoff_handler.go:131-227` (runHandoff)

此任務改寫 `runHandoff` 的 Steps 2-4（中斷 CC → 擷取 session ID → 退出 CC）。由於 `runHandoff` 是非同步 goroutine 且依賴 tmux 操作，單元測試主要覆蓋 handler 層（已有）。流程正確性需靠整合測試或手動測試驗證。

- [ ] **Step 1: 改寫 runHandoff Step 2 — 使用 SendKeysRaw**

將現有的：
```go
s.tmux.SendKeys(sess.Name, "C-c")
```
替換為：
```go
s.tmux.SendKeysRaw(sess.Name, "C-u")
s.tmux.SendKeysRaw(sess.Name, "C-c")
```

並將輪詢終止條件從 `StatusNormal` 改為 `StatusCCIdle`：
```go
st := s.detector.Detect(sess.Name)
if st == detect.StatusCCIdle {
	break
}
```

- [ ] **Step 2: 新增 Step 3 — 擷取 session ID**

在 Step 2 之後、Step 4（原本的啟動 relay）之前插入：

```go
// Step 3: Extract session ID via /status
broadcast("extracting-id")
if err := s.tmux.SendKeys(sess.Name, "/status"); err != nil {
	broadcast("failed:send /status: " + err.Error())
	return
}
time.Sleep(2 * time.Second)
paneContent, err := s.tmux.CapturePaneContent(sess.Name, 40)
if err != nil {
	broadcast("failed:capture pane: " + err.Error())
	return
}
sessionID, err := detect.ExtractSessionID(paneContent)
if err != nil {
	broadcast("failed:could not extract session ID")
	return
}
```

- [ ] **Step 3: 新增 Step 4 — 退出 CC**

```go
// Step 4: Exit CC
broadcast("exiting-cc")
s.tmux.SendKeysRaw(sess.Name, "Escape")
time.Sleep(500 * time.Millisecond)
if err := s.tmux.SendKeys(sess.Name, "/exit"); err != nil {
	broadcast("failed:send /exit: " + err.Error())
	return
}
exitDeadline := time.Now().Add(10 * time.Second)
for time.Now().Before(exitDeadline) {
	time.Sleep(500 * time.Millisecond)
	if s.detector.Detect(sess.Name) == detect.StatusNormal {
		break
	}
}
if s.detector.Detect(sess.Name) != detect.StatusNormal {
	broadcast("failed:CC did not exit")
	return
}
```

- [ ] **Step 4: 修改 relay command — 加入 --resume**

將組裝 `relayCmd` 的行替換為：
```go
relayCmd := fmt.Sprintf("tbox relay --session %s --daemon ws://127.0.0.1:%d --token-file %s -- %s --resume %s",
	sess.Name, port, tokenFile, command, sessionID)
```

- [ ] **Step 5: 合併 mode + cc_session_id 更新**

將原本 Step 7 的 `s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &mode})` 替換為同時寫入兩個欄位。這避免了 UpdateSession 在只設 CCSessionID 且值未變時回傳 ErrNotFound（因為 Mode 一定會從 term→stream 改變，RowsAffected > 0）：
```go
ccID := sessionID
if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{Mode: &mode, CCSessionID: &ccID}); err != nil {
	broadcast("failed:db update: " + err.Error())
	return
}
```
移除原本獨立的 `UpdateSession(sess.ID, store.SessionUpdate{Mode: &mode})` 呼叫。

- [ ] **Step 6: 新增前提檢查**

在 `runHandoff` 的 Step 1（relay disconnect）**之後**、Step 2（中斷 CC）**之前**新增：
```go
// Prerequisite: CC must be running
broadcast("detecting")
status := s.detector.Detect(sess.Name)
if status == detect.StatusNormal || status == detect.StatusNotInCC {
	broadcast("failed:no CC running")
	return
}
```

注意：此檢查在 relay 斷開之後，因為先斷 relay 再檢查 CC 狀態才準確。

同時移除原本的 Step 2（detect）和 Step 3（stop CC to shell），因為已被新的 Step 2（中斷到 idle）+ Step 3（擷取 ID）+ Step 4（/exit）取代。
確保原本的 `broadcast("stopping-cc")` 保留在新 Step 2 的條件分支中（`if status != detect.StatusCCIdle`）。

- [ ] **Step 7: 更新既有測試 fixture**

`TestHandoffHappyPath` 的 `newHandoffTestServer` 設定 `paneCommands["test-session"] = "zsh"`（StatusNormal），但改寫後 `runHandoff` 要求 CC 在執行中才能 handoff。需更新 fixture：

在 `newHandoffTestServer` 中修改：
```go
fakeTmux.SetPaneCommand("test-session", "claude")  // CC running
fakeTmux.SetPaneContent("test-session", "Session: deadbeef-1234-5678-9abc-def012345678\n❯ ")  // /status 輸出 + idle prompt
```

這讓 detector 偵測為 `cc-idle`，且 `ExtractSessionID` 能解析 session ID。

注意：因為 FakeExecutor 的 `SendKeys`/`SendKeysRaw` 是 no-op，且 detector 每次呼叫都回傳相同狀態，部分輪詢邏輯（等 StatusNormal）在 FakeExecutor 下會超時。測試主要驗證 HTTP handler 層行為（202 回應、handoff_id），而非完整的非同步流程。

- [ ] **Step 8: 執行測試確認不 break**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/server/ -v -run TestHandoff`
Expected: all PASS

- [ ] **Step 8: Commit**

```bash
git add internal/server/handoff_handler.go
git commit -m "feat: rewrite runHandoff with session ID extraction and --resume"
```

---

## Task 6: handoff_handler — runHandoffToTerm

**Files:**
- Modify: `internal/server/handoff_handler.go` (replace stub)

- [ ] **Step 1: 實作 runHandoffToTerm**

替換 Task 4 建立的 stub：

```go
func (s *Server) runHandoffToTerm(sess store.Session, handoffID string) {
	defer s.handoffLocks.Unlock(sess.Name)

	broadcast := func(value string) {
		s.events.Broadcast(sess.Name, "handoff", value)
	}

	// Step 1: Get session ID from DB
	current, err := s.store.GetSession(sess.ID)
	if err != nil || current.CCSessionID == "" {
		broadcast("failed:no session ID available")
		return
	}
	sessionID := current.CCSessionID

	// Step 2: Shut down relay
	if s.bridge.HasRelay(sess.Name) {
		broadcast("stopping-relay")
		s.bridge.SubscriberToRelay(sess.Name, []byte(`{"type":"shutdown"}`))
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if !s.bridge.HasRelay(sess.Name) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if s.bridge.HasRelay(sess.Name) {
			broadcast("failed:relay did not disconnect")
			return
		}
	}

	// Step 3: Wait for shell
	broadcast("waiting-shell")
	shellDeadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(shellDeadline) {
		if s.detector.Detect(sess.Name) == detect.StatusNormal {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if s.detector.Detect(sess.Name) != detect.StatusNormal {
		broadcast("failed:shell did not recover")
		return
	}

	// Step 4: Launch interactive CC with --resume
	broadcast("launching-cc")
	resumeCmd := fmt.Sprintf("claude --resume %s", sessionID)
	if err := s.tmux.SendKeys(sess.Name, resumeCmd); err != nil {
		broadcast("failed:send-keys error: " + err.Error())
		return
	}

	// Step 5: Verify CC started
	ccDeadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(ccDeadline) {
		st := s.detector.Detect(sess.Name)
		if st == detect.StatusCCIdle || st == detect.StatusCCRunning || st == detect.StatusCCWaiting {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	finalSt := s.detector.Detect(sess.Name)
	if finalSt != detect.StatusCCIdle && finalSt != detect.StatusCCRunning && finalSt != detect.StatusCCWaiting {
		broadcast("failed:CC did not start")
		return
	}

	// Step 6: Update DB
	termMode := "term"
	emptyID := ""
	if err := s.store.UpdateSession(sess.ID, store.SessionUpdate{
		Mode:        &termMode,
		CCSessionID: &emptyID,
	}); err != nil {
		broadcast("failed:db update error: " + err.Error())
		return
	}
	broadcast("connected")
}
```

- [ ] **Step 2: 執行全部測試**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./internal/server/ -v -run TestHandoff`
Expected: all PASS

- [ ] **Step 3: 執行全專案測試**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./...`
Expected: all PASS

- [ ] **Step 4: Commit**

```bash
git add internal/server/handoff_handler.go
git commit -m "feat: implement runHandoffToTerm for stream-to-term handoff"
```

---

## Task 7: Frontend — api.ts 修改

**Files:**
- Modify: `spa/src/lib/api.ts:2-11` (Session interface), `:48-61` (handoff function)

- [ ] **Step 1: 修改 Session interface**

```typescript
export interface Session {
  id: number
  uid: string
  name: string
  tmux_target: string
  cwd: string
  mode: string
  group_id: number
  sort_order: number
  cc_session_id: string
}
```

- [ ] **Step 2: 修改 handoff() — preset 改 optional**

```typescript
export async function handoff(
  base: string,
  id: number,
  mode: string,
  preset?: string,
): Promise<{ handoff_id: string }> {
  const body: Record<string, string> = { mode }
  if (preset) body.preset = preset
  const res = await fetch(`${base}/api/sessions/${id}/handoff`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) throw new Error(`handoff failed: ${res.status}`)
  return res.json()
}
```

- [ ] **Step 3: 確認 TypeScript 編譯**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 4: Commit**

```bash
git add spa/src/lib/api.ts
git commit -m "feat: make handoff preset optional, add cc_session_id to Session"
```

---

## Task 8: Frontend — HandoffButton 更名 + 啟用條件 + 進度標籤

**Files:**
- Modify: `spa/src/components/HandoffButton.tsx`

- [ ] **Step 1: 更新 HandoffButton**

替換整個 `HandoffButton.tsx`：

```tsx
import { Terminal } from '@phosphor-icons/react'
import type { HandoffState } from '../stores/useStreamStore'

interface Props {
  state: HandoffState
  progress?: string
  sessionStatus?: string
  onHandoff: () => void
}

function progressLabel(progress: string): string {
  switch (progress) {
    case 'detecting': return 'Detecting CC...'
    case 'stopping-cc': return 'Stopping CC...'
    case 'extracting-id': return 'Extracting session...'
    case 'exiting-cc': return 'Exiting CC...'
    case 'launching': return 'Launching relay...'
    case 'stopping-relay': return 'Stopping relay...'
    case 'waiting-shell': return 'Waiting for shell...'
    case 'launching-cc': return 'Launching CC...'
    default: return progress || 'Connecting...'
  }
}

function isCCRunning(status?: string): boolean {
  return status === 'cc-idle' || status === 'cc-running' || status === 'cc-waiting'
}

export default function HandoffButton({ state, progress = '', sessionStatus, onHandoff }: Props) {
  if (state === 'connected') return null

  const ccAvailable = isCCRunning(sessionStatus)
  const inProgress = state === 'handoff-in-progress'
  const disabled = inProgress || !ccAvailable

  return (
    <div className="flex flex-col items-center justify-center h-full gap-3">
      <button
        onClick={onHandoff}
        disabled={disabled}
        className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 text-white text-sm hover:bg-blue-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        <Terminal size={16} />
        {inProgress ? progressLabel(progress) : 'Handoff'}
      </button>
      {state === 'disconnected' && (
        <p className="text-xs text-gray-500">Session disconnected. Click to reconnect.</p>
      )}
      {!ccAvailable && !inProgress && (
        <p className="text-xs text-gray-500">No CC running</p>
      )}
    </div>
  )
}
```

- [ ] **Step 2: 更新 ConversationView 中的 HandoffButton 呼叫**

在 `spa/src/components/ConversationView.tsx`，HandoffButton 的 props 需要調整：

移除 `presetName` prop，新增 `sessionStatus` prop。在 `ConversationView` 的 Props 中新增 `sessionStatus?: string`，然後傳遞：

```tsx
<HandoffButton
  state={handoffState}
  progress={handoffProgress}
  sessionStatus={sessionStatus}
  onHandoff={handleHandoff}
/>
```

ConversationView Props interface 新增：
```typescript
interface Props {
  wsUrl: string
  sessionName: string
  presetName: string  // 保留供 App.tsx 使用
  sessionStatus?: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}
```

- [ ] **Step 3: 確認 TypeScript 編譯**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx tsc --noEmit`
Expected: no errors（可能需要同時更新 App.tsx 傳入的 props — 見 Task 10）

- [ ] **Step 4: Commit**

```bash
git add spa/src/components/HandoffButton.tsx spa/src/components/ConversationView.tsx
git commit -m "feat: rename HandoffButton to Handoff, add CC status check and progress labels"
```

---

## Task 9: Frontend — StreamInput "Handoff to Term" 按鈕

**Files:**
- Modify: `spa/src/components/StreamInput.tsx`

- [ ] **Step 1: 新增按鈕和 prop**

修改 `StreamInput.tsx`：

新增 import：
```tsx
import { Plus, Terminal } from '@phosphor-icons/react'
```

Props interface 新增：
```typescript
interface Props {
  onSend: (text: string) => void
  onAttach?: () => void
  onHandoffToTerm?: () => void
  disabled?: boolean
  placeholder?: string
}
```

修改底部工具欄：
```tsx
<div className="flex items-center px-2 pb-1.5">
  <button
    type="button"
    disabled={disabled}
    onClick={onAttach}
    className="w-7 h-7 rounded-md flex items-center justify-center text-[#666] hover:text-[#ddd] hover:bg-[#333] transition-colors disabled:opacity-40"
  >
    <Plus size={16} />
  </button>
  <div className="flex-1" />
  {onHandoffToTerm && (
    <button
      type="button"
      onClick={onHandoffToTerm}
      title="Handoff to Term"
      className="flex items-center gap-1 px-2 py-1 rounded text-xs text-[#888] hover:text-[#ddd] hover:bg-[#333] transition-colors"
    >
      <Terminal size={14} />
      <span>Handoff to Term</span>
    </button>
  )}
</div>
```

- [ ] **Step 2: 確認 TypeScript 編譯**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 3: Commit**

```bash
git add spa/src/components/StreamInput.tsx
git commit -m "feat: add Handoff to Term button in StreamInput toolbar"
```

---

## Task 10: Frontend — App.tsx 雙 View 掛載 + handleHandoffToTerm

**Files:**
- Modify: `spa/src/App.tsx`
- Modify: `spa/src/components/ConversationView.tsx` (clear() 保護)

- [ ] **Step 1: ConversationView — clear() 保護**

在 `spa/src/components/ConversationView.tsx`，修改 useEffect：

```tsx
const prevWsUrlRef = useRef<string>('')

useEffect(() => {
  // Only clear when switching to a different session, not on re-mount
  if (prevWsUrlRef.current && prevWsUrlRef.current !== wsUrl) {
    clear()
  }
  prevWsUrlRef.current = wsUrl

  const conn = connectStream(
    // ... rest of existing logic unchanged
  )
  // ...
}, [wsUrl])
```

新增 `useRef` import（應已有）。

- [ ] **Step 2: ConversationView — 傳遞 onHandoffToTerm 到 StreamInput**

在 `ConversationView` 的 render 中，StreamInput 新增 prop：

```tsx
<StreamInput
  onSend={handleSend}
  onAttach={handleAttach}
  onHandoffToTerm={onHandoffToTerm}
  disabled={isStreaming}
/>
```

- [ ] **Step 3: App.tsx — 雙 View 掛載**

**xterm.js fit() 說明**：TerminalView 已內建 `ResizeObserver`（`TerminalView.tsx:64-68`），當容器從 `display: none`（尺寸 0）切回 `display: block`（尺寸恢復），ResizeObserver 會自動觸發 `fitAddon.fit()`。無需額外處理。

將 `App.tsx` 中的條件渲染區塊（約 L138-156）替換為：

```tsx
<div className="flex-1 overflow-hidden">
  {active ? (
    <>
      <div style={{ display: currentMode === 'term' ? 'block' : 'none', height: '100%' }}>
        <TerminalView
          wsUrl={`${wsBase}/ws/terminal/${encodeURIComponent(active.name)}`}
        />
      </div>
      <div style={{
        display: currentMode === 'stream' ? 'flex' : 'none',
        flexDirection: 'column',
        height: '100%',
      }}>
        <ConversationView
          wsUrl={`${wsBase}/ws/cli-bridge-sub/${encodeURIComponent(active.name)}`}
          sessionName={active.name}
          presetName={activePreset || streamPresets[0]?.name || 'cc'}
          sessionStatus={useStreamStore.getState().sessionStatus[active.name]}
          onHandoff={() => handleHandoff('stream', activePreset || streamPresets[0]?.name || 'cc')}
          onHandoffToTerm={handleHandoffToTerm}
        />
      </div>
    </>
  ) : (
    <div className="flex items-center justify-center h-full">
      <p className="text-gray-400">Select a session</p>
    </div>
  )}
</div>
```

- [ ] **Step 4: App.tsx — handleHandoffToTerm handler**

在 `handleHandoff` 之後新增：

```tsx
const handleHandoffToTerm = useCallback(async () => {
  if (!active) return
  try {
    useStreamStore.getState().setHandoffState('handoff-in-progress')
    await handoff(daemonBase, active.id, 'term')
    setHash(active.uid, 'term')
    await fetchSessions(daemonBase)
  } catch (e) {
    console.error('handoff to term failed:', e)
  }
}, [active, fetchSessions])
```

**Spec 差異說明**：Spec 提到呼叫後 `setHandoffState('idle')`，但此處刻意不設。
stream view 的狀態重置由後端 SSE `connected` 事件觸發（App.tsx session-events handler 已處理）。
若立即設 idle 會導致 HandoffButton 在 stream view 中閃現。

- [ ] **Step 5: App.tsx — sessionStatus 用 reactive 取法**

`useStreamStore.getState()` 是 non-reactive 呼叫。為了讓 HandoffButton 在 sessionStatus 變化時更新，改用 `useStreamStore` 的 selector：

在 App 組件開頭新增：
```tsx
const sessionStatus = useStreamStore((s) =>
  active ? s.sessionStatus[active.name] : undefined
)
```

然後傳入 ConversationView：
```tsx
sessionStatus={sessionStatus}
```

- [ ] **Step 6: 確認 TypeScript 編譯**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && npx tsc --noEmit`
Expected: no errors

- [ ] **Step 7: Build 測試**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm build`
Expected: build 成功

- [ ] **Step 8: Commit**

```bash
git add spa/src/App.tsx spa/src/components/ConversationView.tsx
git commit -m "feat: dual View mount, handleHandoffToTerm, clear() protection"
```

---

## Task 11: 全專案驗證

- [ ] **Step 1: Go 全部測試**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go test ./...`
Expected: all PASS

- [ ] **Step 2: 前端 Build**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm build`
Expected: build 成功

- [ ] **Step 3: 前端 Lint**

Run: `cd /Users/wake/Workspace/wake/tmux-box/spa && pnpm lint`
Expected: no errors

- [ ] **Step 4: Go Build**

Run: `cd /Users/wake/Workspace/wake/tmux-box && go build ./cmd/tbox/`
Expected: build 成功

- [ ] **Step 5: 手動整合測試（需 tmux + CC 環境）**

1. 啟動 daemon: `tbox serve`
2. 開啟 SPA
3. 在 tmux session 中啟動 CC（互動模式）
4. 在 SPA 點 stream → Handoff 按鈕 → 確認 handoff-to-stream 成功
5. 在 stream 模式中 → 點 "Handoff to Term" → 確認回到 term 看到互動式 CC
6. 在 term 模式中 → 再次 handoff to stream → 確認雙向循環正常
