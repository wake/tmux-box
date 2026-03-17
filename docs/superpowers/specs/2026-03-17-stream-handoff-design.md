# Stream Handoff 雙向切換設計

## 概述

在 term（互動式 CC）與 stream（`-p` 串流模式）之間實現雙向 handoff，使用 `--resume <session_id>` 精確續接同一 CC 對話。使用者可在兩種模式間自由切換，不遺失對話脈絡。

## 前提條件

Handoff **僅在 CC 正在執行時可用**（`cc-idle` / `cc-running` / `cc-waiting`）。若 tmux session 處於 shell（`normal`）或執行非 CC 程式（`not-in-cc`），handoff 按鈕 disabled，不可觸發。

---

## Handoff to Stream（term → stream）

### 觸發方式

使用者在 stream 頁面點擊 "Handoff" 按鈕（原 "Start {preset}" 按鈕更名）。

### 後端流程（`handoff_handler.go` — 修改現有 `runHandoff`）

本次修改**取代**現有 `runHandoff` 的 Steps 2-4。現有流程直接 `C-c` → 等 shell → 啟動 fresh `claude -p`；新流程改為：中斷到 cc-idle → 擷取 session ID → `/exit` 退出 → 啟動 `claude -p --resume`。由於前提條件要求 CC 必須在執行中，原本的「CC 未執行時啟動 fresh relay」流程不再適用於 handoff 按鈕。

```
前提檢查: detect(session) ∈ {cc-idle, cc-running, cc-waiting}
         否則 → broadcast("failed:no CC running") 並返回

Step 1 — 斷開現有 relay（若有）
  與現有 runHandoff Step 1 邏輯相同（送 shutdown → 輪詢等 5s relay 斷開）

Step 2 — 中斷進行中的工作（若非 idle）
  若 status = cc-running 或 cc-waiting:
    tmux SendKeysRaw C-u          // 清空已輸入但未送出的文字
    tmux SendKeysRaw C-c          // 中斷當前任務
    輪詢等待 cc-idle (❯)，最多 10s
    若超時 → broadcast("failed:could not reach CC idle")

  注意：C-u 只在有輸入區時有意義。cc-running 時 CC 可能無輸入區，
  但 C-u 在此情境下為 no-op，不會造成副作用，統一送出可簡化邏輯。

Step 3 — 擷取 session ID
  broadcast("extracting-id")
  tmux SendKeys "/status"         // 注入 /status 指令（需帶 Enter）
  等待 2s（讓 CC 輸出 status 資訊）
  tmux CapturePaneContent(session, 40)  // 取最近 40 行
  解析 session_id（見「/status 輸出解析」章節）
  若解析失敗 → broadcast("failed:could not extract session ID")

Step 4 — 退出 CC
  broadcast("exiting-cc")
  tmux SendKeysRaw Escape         // 跳離 /status 顯示畫面
  sleep 500ms
  tmux SendKeys "/exit"           // 優雅退出 CC（需帶 Enter）
  輪詢等待 StatusNormal (shell)，最多 10s
  若超時 → broadcast("failed:CC did not exit")

Step 5 — 啟動 relay
  broadcast("launching")
  寫入 token 臨時檔案（C3 安全，與現有邏輯相同）
  組裝指令:
    tbox relay --session {name} --daemon ws://127.0.0.1:{port} \
      --token-file {path} -- \
      claude -p --input-format stream-json --output-format stream-json \
      --resume {session_id}
  tmux SendKeys 注入指令（需帶 Enter）

Step 6 — 等待 relay 回連
  輪詢 bridge.HasRelay(session)，最多 15s
  relay init 訊息中的 session_id 由前端 store 接收

Step 7 — 更新狀態
  DB: session.mode = "stream"
  DB: session.cc_session_id = {session_id}
  broadcast("connected")
```

### 事件廣播順序

```
detecting → stopping-cc (若需要) → extracting-id → exiting-cc → launching → connected
```

---

## Handoff to Term（stream → term）

### 觸發方式

使用者在 stream 模式下方工具欄最右側點擊 "Handoff to Term" 按鈕。

### API 端點

擴充現有 `POST /api/sessions/{id}/handoff`：

```json
// Handoff to stream（現有行為）
{ "mode": "stream", "preset": "cc" }

// Handoff to term（新增）
{ "mode": "term" }
```

後端分流邏輯：
- `mode ∈ {"stream", "jsonl"}` → 驗證 preset 存在 → `runHandoff()`（現有流程 + 本次修改）
- `mode = "term"` → 不需要 preset → `runHandoffToTerm()`

後端驗證修改（`handoff_handler.go`）：
```go
// 原本
if req.Mode != "stream" && req.Mode != "jsonl" { ... }
// 改為
if req.Mode != "stream" && req.Mode != "jsonl" && req.Mode != "term" { ... }

// preset 驗證只在 stream/jsonl 時執行
if req.Mode != "term" {
    // find preset command...
}
```

前端 API 修改（`api.ts`）：
```typescript
// preset 改為 optional
export async function handoff(
  base: string, id: number, mode: string, preset?: string,
): Promise<{ handoff_id: string }> {
  const body: Record<string, string> = { mode }
  if (preset) body.preset = preset
  // ...
}
```

### 後端流程（新增 `runHandoffToTerm`）

```
共用 handoffLocks — 與 runHandoff 使用同一個 per-session mutex

Step 1 — 取得 session_id
  從 DB (session.cc_session_id) 取得
  若為空 → broadcast("failed:no session ID available")

Step 2 — 關閉 relay
  broadcast("stopping-relay")
  bridge.SubscriberToRelay(session, {"type":"shutdown"})
  輪詢等 relay 斷開（最多 5s）
  若超時 → broadcast("failed:relay did not disconnect")

Step 3 — 等待 shell
  broadcast("waiting-shell")
  輪詢 detect(session) = StatusNormal，最多 10s
  （relay 結束後 claude -p 也會退出，shell 應很快恢復）
  若超時 → broadcast("failed:shell did not recover")

Step 4 — 注入互動式 CC
  broadcast("launching-cc")
  tmux SendKeys "claude --resume {session_id}"（需帶 Enter）

Step 5 — 驗證 CC 啟動
  輪詢 detect(session) ∈ {cc-idle, cc-running, cc-waiting}，最多 15s
  若超時 → broadcast("failed:CC did not start")

Step 6 — 更新狀態
  DB: session.mode = "term"
  DB: session.cc_session_id = ""  // 清除，避免過時
  broadcast("connected")
```

### 事件廣播順序

```
stopping-relay → waiting-shell → launching-cc → connected
```

---

## 事件廣播完整對照表

| 方向 | 進度事件序列 | 成功事件 | 失敗前綴 |
|------|-------------|---------|---------|
| to-stream | `detecting` → `stopping-cc` → `extracting-id` → `exiting-cc` → `launching` | `connected` | `failed:*` |
| to-term | `stopping-relay` → `waiting-shell` → `launching-cc` | `connected` | `failed:*` |

兩個方向的成功事件統一為 `connected`，前端不需要區分方向。

---

## tmux Executor 介面擴充

現有 `SendKeys` 自動附加 `Enter`，但 handoff 流程需要送出不帶 Enter 的控制鍵（`C-u`、`C-c`、`Escape`）。

### 新增方法

```go
// Executor interface 新增
SendKeysRaw(target string, keys ...string) error

// RealExecutor 實作
func (r *RealExecutor) SendKeysRaw(target string, keys ...string) error {
    args := []string{"send-keys", "-t", target}
    args = append(args, keys...)
    return exec.Command("tmux", args...).Run()
}
```

**使用時機**：
- `SendKeys` → 送出需要 Enter 的指令（`/status`、`/exit`、relay 啟動指令、`claude --resume`）
- `SendKeysRaw` → 送出控制鍵（`C-u`、`C-c`、`Escape`）

`FakeExecutor` 也需要對應新增此方法。

**遷移既有程式碼**：現有 `runHandoff` 中的 `s.tmux.SendKeys(sess.Name, "C-c")`（`handoff_handler.go:170`）也應改用 `SendKeysRaw`，因為 `C-c` 是控制鍵不應附帶 Enter。

---

## /status 輸出解析

### 格式範例（需實際環境驗證）

CC `/status` 的預期輸出類似：

```
╭─ Status ──────────────────────────────────╮
│ Session ID: 4dd75bf4-98e6-4f08-b753-08153d91c5fa │
│ Model: claude-sonnet-4-20250514             │
│ Tools: Read, Edit, Write, Bash, Glob ...  │
│ Context: 45,231 / 200,000 tokens          │
╰───────────────────────────────────────────╯
```

### 解析策略

在 `internal/detect/` 中新增函式：

```go
func ExtractSessionID(paneContent string) (string, error)
```

解析邏輯：
1. 逐行掃描 pane 內容
2. 匹配 `Session ID:` 關鍵字後的 UUID 格式（`[0-9a-f]{8}-...-[0-9a-f]{12}`）
3. 若無匹配 → 回傳 error

正規表達式草案：
```go
var sessionIDRegex = regexp.MustCompile(`Session ID:\s*([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})`)
```

**重要**：此格式基於推測，**實作前必須在實際環境中執行 `/status` 並記錄輸出**。若格式不符預期，需調整正規表達式。建議在 implementation plan 中將此列為第一步的驗證任務。同時驗證 `/status` 輸出後是否需要 `Escape` 跳離（若為 inline 輸出則 Escape 為 no-op，無害但可節省 500ms 延遲）。

### 脆弱性緩解

- `/status` 是 CC 的穩定指令，不太可能被移除
- 若 CC 更新導致格式變動 → 只需更新 regex，不影響架構
- 擷取前先等 2s 確保 CC 完成輸出
- capture-pane 取 40 行（遠多於 /status 輸出長度），降低截斷風險

---

## 前端變更

### 1. 狀態保留 — 雙 View 同時掛載

**現況**：`App.tsx` 用條件渲染，切頁時卸載非當前 View，導致 WebSocket 斷開 + 狀態清空。

**變更**：TerminalView 與 ConversationView 同時 mount，用 CSS `display` 控制可見性。

```tsx
// App.tsx — 主內容區
<div className="flex-1 overflow-hidden">
  {active && (
    <>
      <div style={{ display: currentMode === 'term' ? 'block' : 'none', height: '100%' }}>
        <TerminalView wsUrl={...} />
      </div>
      <div style={{
        display: currentMode === 'stream' ? 'flex' : 'none',
        flexDirection: 'column', height: '100%'
      }}>
        <ConversationView wsUrl={...} ... />
      </div>
    </>
  )}
</div>
```

**效果**：
- 切到 term 時保留 stream WebSocket 連線與訊息歷史
- 切到 stream 時保留 terminal PTY 連線

**xterm.js fit() 觸發**：`display: none` 切回 `block` 時不會觸發 React lifecycle。使用 `ResizeObserver` 偵測 container 尺寸從 0 變為正值時觸發 `fit()`，或在 `App.tsx` mode 切換時透過 ref callback 手動觸發。

**ConversationView mount 時 clear() 問題**：現有 `useEffect` 在 mount 時呼叫 `clear()`。改為雙 View 同時掛載後，`clear()` 應只在 `wsUrl` 真正變更時觸發（已是現有 dep），不會因 mode 切換而清空。具體做法：用 `useRef` 追蹤 `prevWsUrl`，只在 `wsUrl !== prevWsUrl.current` 時才呼叫 `clear()`。

**已知限制**：`useStreamStore` 是全域單例，同時只能追蹤一個 session 的 stream 狀態。未來分頁模式（下一期）需要改為 per-session store 實例或 Map 結構。此階段暫不處理。

### 2. HandoffButton 更名與啟用條件

```tsx
// 按鈕文字
{state === 'handoff-in-progress' ? progressLabel(progress) : 'Handoff'}
```

**啟用條件**：按鈕僅在 session status 為 `cc-idle` / `cc-running` / `cc-waiting` 時可點擊。其他狀態 disabled + 顯示提示 "No CC running"。

**新增進度標籤**：
- `extracting-id` → "Extracting session..."
- `exiting-cc` → "Exiting CC..."
- `stopping-relay` → "Stopping relay..."
- `waiting-shell` → "Waiting for shell..."
- `launching-cc` → "Launching CC..."

### 3. StreamInput — 新增 "Handoff to Term" 按鈕

在 `StreamInput` 底部工具欄最右側新增按鈕：

```tsx
<div className="flex items-center px-2 pb-1.5">
  <button onClick={onAttach} ...>
    <Plus size={16} />
  </button>
  <div className="flex-1" />
  <button onClick={onHandoffToTerm} title="Handoff to Term"
    className="flex items-center gap-1 px-2 py-1 rounded text-xs text-[#888] hover:text-[#ddd] hover:bg-[#333] transition-colors">
    <Terminal size={14} />
    <span>Handoff to Term</span>
  </button>
</div>
```

Props 新增：`onHandoffToTerm: () => void`

**元件傳遞鏈**：

```
App.tsx
  └─ 新增 handleHandoffToTerm()
       → handoff(daemonBase, active.id, 'term')  // 不帶 preset
       → setHash(active.uid, 'term')
       → useStreamStore.getState().setHandoffState('idle')
  └─ 傳入 ConversationView 作為 onHandoffToTerm prop
       └─ ConversationView 傳入 StreamInput 作為 onHandoffToTerm prop
```

這是一個**獨立於現有 `handleHandoff`** 的新 handler，不需要 preset 參數。

點擊後的完整流程：
1. `App.handleHandoffToTerm()` 呼叫 `handoff(base, id, 'term')` — 不帶 preset
2. 立即切換 hash 到 `#/{uid}/term`（使用者看到 terminal 畫面）
3. 後端非同步執行 `runHandoffToTerm`，進度透過 session-events 推送
4. stream store 重置為 idle（前端收到 `connected` 事件後，或切頁時手動設定）

### 4. TopBar

stream 按鈕行為不變，仍透過 preset 選擇觸發 handoff to stream。

**與 HandoffButton 的關係**：TopBar 的 stream 按鈕是入口（切換到 stream 頁面 + 觸發 handoff），HandoffButton 是 stream 頁面內的操作元件（顯示進度 / 重試）。兩者觸發同一個 `handleHandoff` 函式，不存在功能重複。

---

## 資料模型變更

### sessions 表新增欄位

```sql
ALTER TABLE sessions ADD COLUMN cc_session_id TEXT NOT NULL DEFAULT '';
```

### 遷移策略

沿用現有 `migrate()` 中的 `PRAGMA table_info` 檢測模式：

```go
// 在 migrate() 函式中，uid 遷移邏輯之後新增：
var hasCCSessionID bool
// （複用已有的 PRAGMA 掃描迴圈，或獨立查詢）
if !hasCCSessionID {
    db.Exec("ALTER TABLE sessions ADD COLUMN cc_session_id TEXT NOT NULL DEFAULT ''")
}
```

### Store struct 與 query 更新

**Session struct**：
```go
type Session struct {
    // ...existing fields...
    CCSessionID string `json:"cc_session_id"`
}
```

**SessionUpdate struct**：
```go
type SessionUpdate struct {
    // ...existing fields...
    CCSessionID *string `json:"cc_session_id,omitempty"`
}
```

**SQL query 更新**：
- `ListSessions` / `GetSession` 的 SELECT 加入 `cc_session_id`
- `UpdateSession` 新增 `CCSessionID` 分支
- `CreateSession` 的 INSERT 不需變更（用 DEFAULT ''）

**前端 Session interface**（`api.ts`）：
```typescript
export interface Session {
  // ...existing fields...
  cc_session_id: string
}
```

### cc_session_id 生命週期

| 時機 | 動作 |
|------|------|
| Handoff to Stream Step 7 | 寫入（從 /status 擷取的值） |
| Handoff to Term Step 6 | 清除（設為 ""） |
| Session 建立 | 空值（DEFAULT ''） |

過時風險：使用者在 term 模式手動退出 CC 再開新 session 時，`cc_session_id` 可能已過時。但因為每次 handoff to stream 都會重新擷取（Step 3），不會使用過時值。Handoff to term 使用的是同一輪 handoff to stream 存入的值，不會過時。

---

## 錯誤處理

| 情境 | 處理 |
|------|------|
| CC 未執行 | 前端 disable 按鈕；後端 reject handoff |
| 中斷 CC 超時 (10s) | broadcast `failed:could not reach CC idle` |
| 解析 session ID 失敗 | broadcast `failed:could not extract session ID` |
| CC 退出超時 (10s) | broadcast `failed:CC did not exit` |
| Relay 未回連 (15s) | broadcast `failed:relay did not connect within 15s` |
| DB 無 session ID | broadcast `failed:no session ID available` |
| Relay 關閉超時 (5s) | broadcast `failed:relay did not disconnect` |
| Shell 未恢復 (10s) | broadcast `failed:shell did not recover` |
| CC 啟動失敗 (15s) | broadcast `failed:CC did not start` |

所有失敗狀態透過 `/ws/session-events` 的 `handoff` 事件推送，前端顯示錯誤並重置為可重試狀態。

---

## 不在此次範圍

- 分頁模式 / per-session store（下一期）
- JSONL 模式 handoff
- WebSocket 自動重連
- 從 CC filesystem 讀取 session state（替代 /status 解析的備案）
