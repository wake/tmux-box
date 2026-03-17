# Stream Mode Redesign — Design Spec

**Date:** 2026-03-17
**Version:** 0.3.0 (Phase 2.5)
**Status:** Approved

## Overview

重新設計 stream 模式的架構與 UI。核心改動：daemon 不再直接 spawn `claude -p` subprocess，改由 `tbox relay` 在 tmux session 內執行，透過 WebSocket 回連 daemon 橋接資料。UI 全面翻新為 Claude Code 風格。

## 架構改動

### 現有架構（v0.2.0）

```
SPA ←WS→ daemon ←stdin/stdout→ claude -p (獨立 subprocess)
                                 ↑ daemon 直接 spawn，與 tmux 無關
```

### 新架構

```
SPA ←WS→ daemon ←WS← tbox relay ←stdin/stdout→ claude -p
                        ↑ 跑在 tmux session 內
                        ↑ 使用者在 term 分頁可見
```

```
┌─ tmux session ─────────────────────────────────┐
│                                                  │
│  tbox relay --session X -- claude -p {flags}     │
│    ├─ stdin  ← daemon WS (user messages)         │
│    ├─ stdout → daemon WS (NDJSON responses)      │
│    └─ stdout → terminal (tee, term 分頁可見)      │
│                                                  │
└──────────────────────────────────────────────────┘
```

### Terminal 分頁與 Stream 分頁的關係

- **term 分頁**：始終是 tmux PTY 串流，不受模式切換影響
- **stream/jsonl 分頁**：接收 `tbox relay` 透過 daemon 轉送的結構化資料
- 切到 term 分頁可以看到 `tbox relay` 指令在跑
- 在 term 分頁 Ctrl+C 可以中斷 `tbox relay`，stream/jsonl 分頁會收到斷線

## Go Daemon 改動

### 刪除

- `internal/stream/session.go` — daemon 直接 spawn claude 的邏輯
- `internal/stream/manager.go` — stream session 管理器
- `internal/server/stream_handler.go` — 舊的 `/ws/stream/` 端點
- `SwitchMode` 中啟動/停止 stream subprocess 的邏輯

### 新增 `tbox relay` 子命令

**用途**：橋接使用者的 claude 指令與 daemon

**介面**：
```bash
tbox relay --session <name> [--daemon <addr>] -- <command...>
```

**Token 傳遞**：透過環境變數 `TBOX_TOKEN` 傳遞，不在命令列中暴露。handoff 端點注入指令時以 `TBOX_TOKEN=... tbox relay ...` 形式寫入（tmux send-keys 不會被 capture-pane 看到，因為指令已經進入 shell history 而非螢幕）。使用者手動執行時需自行設定環境變數或在 shell profile 中 export。

**行為**：
1. 以子進程 spawn `--` 後面的完整指令
2. 接管子進程的 stdin/stdout pipe
3. WebSocket 回連 daemon `ws://{addr}/ws/cli-bridge/{session}`，帶 `TBOX_TOKEN` 認證
4. 雙向橋接：daemon WS ↔ subprocess stdin/stdout
5. subprocess stdout tee 到 stderr（使用者在 term 分頁可見原始 NDJSON 輸出，`tbox relay` 自身的狀態訊息也走 stderr）
6. 子進程結束時通知 daemon 並自行退出
7. 收到 SIGINT/SIGTERM 時轉送給子進程
8. 與 daemon 的 WS 連線斷開時，向子進程發送 SIGTERM（graceful shutdown）

### 新增 `/ws/cli-bridge/{session}` 端點

- 接受 `tbox relay` 的 WebSocket 回連
- Token 認證（復用現有 middleware）
- 原封不動透傳 NDJSON（daemon 不做解析/包裝），SPA 直接收到 claude 的原始輸出
- SPA 發送的訊息原封不動轉送回 `tbox relay` → subprocess stdin
- 同一時間只允許一個 `tbox relay` 連線（同 session）
- daemon 層的控制訊息（session status 變化、handoff 通知）透過獨立的 `/ws/session-events` 端點推送，不混入 cli-bridge 資料流

### 新增 `/api/sessions/{id}/handoff` 端點

**Request**：
```json
{
  "mode": "stream",
  "preset": "cc"
}
```

**行為**：
1. 取得 handoff 鎖（per session mutex，防止並發 handoff）
2. 從 config 查找對應 preset 的 command
3. 偵測 tmux session 當前狀態（`pane_current_command`）
4. 如果有 CC 在跑 → `tmux send-keys -t {session} C-c` → 輪詢等待 `pane_current_command` 回到 shell（timeout 10 秒）
5. 如果 timeout 未成功結束 CC → 回傳失敗，不繼續
6. `tmux send-keys -t {session} "TBOX_TOKEN={token} tbox relay --session {name} -- {command}" Enter`
7. 等待 `tbox relay` 回連 `/ws/cli-bridge/` 確認啟動成功（timeout 15 秒）
8. 釋放 handoff 鎖，回傳結果

**非同步**：handoff 可能需要數秒，回傳 202 Accepted + handoff ID，SPA 透過 WS 接收完成通知。

**模式切換**：
- stream/jsonl → term：SPA 切到 term 分頁即可，`tbox relay` 繼續跑（使用者可在 term 分頁 Ctrl+C 手動結束）
- term → stream/jsonl：觸發 handoff
- stream preset A → stream preset B：先向當前 `tbox relay` 發送中斷信號（透過 cli-bridge WS 傳送 `{"type":"shutdown"}`），等待結束後再 handoff 新 preset

### 新增 Session 狀態偵測

**背景 goroutine**，每 1-2 秒（可設定）對有 SPA client 訂閱的 session 輪詢（無訂閱者的 session 不輪詢以節省資源）：

**偵測方式**（A+B 混合）：

- **B: tmux 命令偵測**（定期校正）
  - `tmux list-panes -t {session} -F '#{pane_current_command}'` → 判斷前景進程
  - 比對 `config.detect.cc_commands` 清單
  - `tmux capture-pane -t {session} -p -S -5` → 抓畫面快照 pattern matching
- **A: PTY 即時解析**
  - 從 terminal relay 已有的 PTY 資料流中解析明顯 pattern
  - 偵測到 CC 特徵時立即更新狀態

**狀態**：
| 狀態 | 說明 |
|------|------|
| `normal` | Shell 閒置 |
| `not-in-cc` | Shell 在跑非 CC 指令 |
| `cc-idle` | CC 開著，等待下一輪指令 |
| `cc-running` | CC 正在工作（工具呼叫、生成中） |
| `cc-waiting` | CC 等待使用者輸入（permission/ask） |
| `cc-unread` | CC 有新輸出（自上次 SPA 連線 stream WS 後有新 assistant message） |

**推送**：狀態變化時透過 WebSocket 推送給 SPA。

### 新增 Config API

- `GET /api/config` — 讀取當前 config（過濾敏感欄位如 token）
- `PUT /api/config` — 更新 config 並寫回 `config.toml`
- 支援的可編輯欄位：`stream.presets`、`jsonl.presets`、`detect.cc_commands`、`detect.poll_interval`

## Config 改動

以下僅列出新增/變更欄位。現有欄位（`bind`、`port`、`token`、`allow`、`data_dir`）不變。

```toml
# ... 現有欄位省略 ...

[[stream.presets]]
name = "cc"
command = "claude -p --input-format stream-json --output-format stream-json"

[[stream.presets]]
name = "dangerous"
command = "claude -p --input-format stream-json --output-format stream-json --dangerously-skip-permissions"

# JSONL presets 預留框架，Phase 3 實作時定義具體 command 格式
# jsonl 的啟動機制可能與 stream 不同（JournalWatcher vs tbox relay），待 Phase 3 設計
[[jsonl.presets]]
name = "cc"
command = ""  # Phase 3 定義

[detect]
cc_commands = ["claude"]
poll_interval = 2
```

## SPA 改動

### 組件重寫

**`MessageBubble`**
- 移除頭像（User/Robot icon）
- 移除 flex-row-reverse 佈局
- User 氣泡靠右、assistant 靠左，無身份標示
- 無裝飾，純內容

**`ToolCallBlock`**
- 保持摺疊式
- 樣式微調配合新色系

**`StreamInput` → 卡片式重寫**
- 外層卡片容器（圓角邊框）
- 上方 textarea，自動隨內容長高（無上限）
- 下方 toolbar：左側 `+` 附件按鈕
- 無送出按鈕
- Enter 送出 / Shift+Enter 換行

**`PermissionPrompt`**
- 橫排佈局：左側描述（icon + tool name + command）、右側 Allow/Deny 按鈕

**`AskUserQuestion`**
- 移除 Submit/Cancel 按鈕
- Options 模式：點選後 Enter 確認
- Free-text 模式（options 為空時）：顯示文字輸入框，Enter 送出

### 新增組件

**`ThinkingIndicator`**
- 三個藍色跳動圓點（`text-blue-400` 系列）
- 放在 assistant 氣泡風格的容器內
- 送出訊息後立即顯示，收到第一條 assistant 回應後隱藏

**`FileAttachment`**
- 懸浮在輸入框卡片上方外部
- 支援拖放上傳（全螢幕 "Drop files here" overlay）
- 支援 `+` 按鈕選擇檔案
- 每個附件顯示縮圖（圖片）或檔案圖示 + 檔名 + × 移除

**`HandoffButton`**
- 顯示在 stream/jsonl 分頁未連線時
- 觸發 `/api/sessions/{id}/handoff`
- 顯示 handoff 進度（偵測中 → 關閉 CC → 啟動中 → 連線成功）

**`SessionStatusBadge`**
- 在 SessionPanel 的每個 session 旁顯示即時狀態徽章
- 顏色對映：normal=灰、not-in-cc=灰、cc-idle=灰綠、cc-running=綠、cc-waiting=黃、cc-unread=藍

### 連線改動

**`stream-ws.ts`**
- WebSocket URL 改為 `/ws/cli-bridge/{session}`
- 協定不變（NDJSON 雙向）

**`useStreamStore`**
- 新增 `handoffState`: `idle` | `handoff-in-progress` | `connected` | `disconnected`
- 新增 `sessionStatus`: 對應 daemon 推送的 session 狀態

### TopBar 改動

```
[term] [stream ▾] [jsonl ▾]
```

- stream / jsonl 按鈕帶下拉選單
- 展開顯示該模式的所有 preset（從 config 讀取）
- 點 preset → 觸發 handoff 帶入對應 command
- 只有一個 preset 時直接執行，不顯示下拉
- 當前 active preset 高亮顯示

### 樣式

- 整體亮度提升約 30%（背景 `#191919`、元件相應調整）
- 藍色統一使用 `text-blue-400`（`#60a5fa`）系列為基礎色
- User 氣泡：`bg-blue-700`（`#1d4ed8`）
- Assistant 氣泡：`bg-gray-800` 提亮版
- 輸入框卡片：`bg-[#242424]`、border `#404040`
- Focus 狀態統一 `border-blue-400`

### 系統設定頁

- CRUD stream presets（name + command）
- CRUD jsonl presets（name + command）
- 編輯 CC 偵測指令清單（`detect.cc_commands`）
- 編輯偵測輪詢間隔（`detect.poll_interval`）
- 透過 `/api/config` 端點讀寫

## Mockup 參考

設計 mockup 存放於 `.superpowers/brainstorm/session-4/`：
- `stream-ui-v6.html` — 最終確認的互動式 mockup
- `stream-ui-v6-float.html` — 含懸浮附件 + 亮度調整版

## 不在範圍

- JSONL 模式的完整實作（本次只建立 preset 框架，實際 JSONL 解析留 Phase 3）
- 檔案上傳的後端處理（前端 UI 支援選擇/拖放檔案，圖片以 base64 嵌入 user message content block 送出；非圖片檔案的處理留後續）
- Streaming output 逐字渲染（技術債，留後續處理）
- WebSocket 重連邏輯（技術債）
