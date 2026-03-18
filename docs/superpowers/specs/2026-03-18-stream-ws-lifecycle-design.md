# Stream WS 連線生命週期重構

## 問題

ConversationView 在 mount 時就建立 cli-bridge-sub WS 連線，但此時 relay 尚未啟動（daemon 回 404）。WS 握手失敗後永遠不會重試。當 handoff 完成、session-events 廣播 `connected` 時，UI 顯示聊天介面，但底層 WS socket 已死 — 使用者送出的訊息被靜默丟棄。

### 根本原因

三層脫節：

1. **UI 狀態**（`handoffState`）由 session-events 驅動，只管該顯示什麼畫面
2. **WS 連線**由 ConversationView useEffect 管理，只在 `wsUrl` 變時觸發，與 handoffState 脫鉤
3. **Component 生命週期** — `display:none` 常駐掛載，WS 在 relay 不存在時就嘗試連線

### 附帶問題

- `useStreamStore` 是全域單例 — 切換 session 時 `clear()` 把一切歸零，對話歷史消失
- Stream resume 時 SPA 看不到之前的對話內容
- CC init message（含 model 資訊）在 subscriber 連線前被 bridge 丟棄

## 設計

### Section 1: Go 後端 — init metadata 攔截 + session-events relay 狀態

#### 1a. handleCliBridge 攔截 init metadata

`bridge_handler.go` 的 relay→subscribers goroutine 加 one-shot 攔截：

- 每條 relay WS 訊息進來時，正常 fan-out 給 subscribers（不變）
- 如果 `!initCaptured` 且訊息是 `type=system, subtype=init`：
  - JSON unmarshal 到輕量 struct，提取 `model`、`session_id`
  - `store.UpdateSession(sessID, {CCModel: model})`
  - `events.Broadcast(session, "init", model)`
  - `initCaptured = true`，之後跳過解析

DB 變更：sessions 表新增 `cc_model TEXT` 欄位（migration）。

#### 1b. session-events 廣播 relay 狀態

新增 `relay` 事件類型：

- `bridge.RegisterRelay` 時 → 廣播 `{"session":"foo","type":"relay","value":"connected"}`
- `bridge.UnregisterRelay` 時 → 廣播 `{"session":"foo","type":"relay","value":"disconnected"}`

Snapshot 擴充：新 subscriber 連線時，除了現有的 detect status snapshot，也送出所有 session 的 relay 狀態（遍歷 `bridge.Sessions()`）。

#### 1c. Session API 回應擴充

`GET /api/sessions` 的每個 session 加：

- `has_relay: bool` — 從 `bridge.HasRelay(name)` 取
- `cc_model: string` — 從 DB 取

這是補充資訊，SPA 的主要資料來源仍是 session-events。

### Section 2: Go 後端 — JSONL 歷史讀取 API

#### 端點

`GET /api/sessions/{id}/history`

#### 路徑解析

CC JSONL 儲存路徑：`~/.claude/projects/{path-hash}/{cc_session_id}.jsonl`

- `path-hash`：工作目錄路徑把 `/` 換成 `-`（例如 `/Users/wake/Workspace/wake/tmux-box` → `-Users-wake-Workspace-wake-tmux-box`）
- `cc_session_id`：DB 已有（handoff step 8 存的）
- 工作目錄：從 session 的 tmux pane `pane_current_path` 取得

#### 回應格式

stream-json 相容的 JSON array：

```json
[
  {"type":"user","message":{"role":"user","content":[{"type":"text","text":"..."}],"stop_reason":null}},
  {"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"..."}],"stop_reason":"end_turn"}}
]
```

#### 轉換規則

- 逐行讀取 JSONL，只取 `type=user` 或 `type=assistant` 且有 `message` 欄位的行
- 提取 `message` 欄位（`{role, content, stop_reason}`）
- 包上 `{type, message}` 外殼
- `content` 如果是 string → 轉成 `[{"type":"text","text":"..."}]`
- 跳過 `progress`、`system`、`file-history-snapshot` 等非對話訊息
- 解析錯誤的行跳過，不中斷

#### 錯誤處理

- `cc_session_id` 為空 → 404
- JSONL 檔案不存在 → 404
- 全量讀取，不做分頁（JSONL 通常幾百 KB）

### Section 3: SPA — per-session store 重構

#### 新 store 結構

```ts
interface PerSessionState {
  messages: StreamMessage[]
  pendingControlRequests: ControlRequest[]
  isStreaming: boolean
  conn: StreamConnection | null
  sessionInfo: { sessionId: string; model: string }
  cost: number
}

interface StreamStore {
  sessions: Record<string, PerSessionState>
  sessionStatus: Record<string, string>
  relayStatus: Record<string, boolean>
  handoffState: Record<string, HandoffState>
  handoffProgress: Record<string, string>

  // actions 全部加 sessionName 參數
  addMessage(session: string, msg: StreamMessage): void
  setConn(session: string, conn: StreamConnection | null): void
  setStreaming(session: string, streaming: boolean): void
  setRelayStatus(session: string, connected: boolean): void
  setHandoffState(session: string, state: HandoffState): void
  setHandoffProgress(session: string, progress: string): void
  loadHistory(session: string, messages: StreamMessage[]): void
  clearSession(session: string): void
}
```

#### 關鍵行為

- **切換 session**：不 clear，保留每個 session 的狀態
- **`loadHistory`**：從 JSONL API 載入的歷史訊息設定為初始 messages
- **`relayStatus`**：由 session-events 的 `relay` 事件更新，驅動 WS 連線決策
- **`handoffState` per-session**：每個 session 獨立追蹤
- **lazy init**：存取不存在的 session key 時自動建立預設值

### Section 4: SPA — App.tsx 管理 stream WS 生命週期

#### 核心原則

stream WS 的建立/銷毀完全由 `relayStatus` 驅動。

#### 連線決策邏輯

```
session-events 收到 relay:connected(sessionName)
  → store.setRelayStatus(sessionName, true)
  → 建立 stream WS（connectStream）
  → store.setConn(sessionName, conn)
  → fetch /api/sessions/{id}/history → store.loadHistory(sessionName, msgs)

session-events 收到 relay:disconnected(sessionName)
  → store.setRelayStatus(sessionName, false)
  → 關閉該 session 的 stream WS
  → store.setConn(sessionName, null)
```

在 App.tsx 現有的 session-events useEffect 中擴充處理 `relay` 事件。

#### 冷啟動

session-events snapshot 送出所有 session 的 relay 狀態。App.tsx 用同一套邏輯處理 snapshot 和即時事件。

#### handoff 事件角色

`handoff` 事件仍驅動 `handoffState`（進度 UI），但不再觸發 WS 連線。時序：

```
relay 註冊 → bridge 廣播 relay:connected → handoff step 8 廣播 handoff:connected
              ↑ WS 在這裡建立                ↑ UI 在這裡顯示 connected
```

### Section 5: SPA — ConversationView 純 UI 重構

#### 移除

- `useEffect` 裡的 `connectStream` → 已移到 App.tsx
- `connRef` → 改從 store 讀 conn
- WS `onMessage` handler → 已移到 App.tsx
- `setHandoffState('disconnected')` on WS close → App.tsx 處理

#### 保留

- 從 per-session store 讀取所有 state
- 渲染對話 UI
- `handleSend`：從 store 取 conn，呼叫 `conn.send()`
- 檔案拖放、附件管理

#### Props

```ts
// 之前
interface Props {
  wsUrl: string
  sessionStatus?: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}

// 之後
interface Props {
  sessionName: string
  onHandoff?: () => void
  onHandoffToTerm?: () => void
}
```

## 測試策略

- Go 後端：TDD — 先寫測試再實作
  - init metadata 攔截測試
  - session-events relay 狀態廣播 + snapshot 測試
  - JSONL 歷史讀取 API 測試（mock JSONL 檔案）
  - E2E pipeline 測試（已有，可擴充）
- SPA：TDD — vitest
  - per-session store 單元測試
  - App.tsx WS lifecycle 整合測試
  - ConversationView 純 UI 測試（不涉及 WS）

## 不在範圍內

- JSONL 分頁載入（未來需要時再加）
- Bridge replay buffer（用 JSONL API 取代）
- stream-ws.ts 加重連邏輯（由 App.tsx relay 事件驅動，不需要）
- Per-session 認證隔離
