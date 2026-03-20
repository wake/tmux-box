# Changelog

## [1.0.0-alpha.1] - 2026-03-20

Phase 1: 分頁系統 + Activity Bar — SPA 架構從「單 session 檢視」升級為「多分頁 + 工作區」

### 新增

- **Tab 系統** — 每個 tmux session 自動對應一個 tab，支援 terminal / stream / editor 三種類型
- **ActivityBar** — 左側垂直工作區切換列（Workspace icons + standalone tabs + 設定入口）
- **TabBar** — 水平分頁列（切換 / 關閉 / 新增 / dirty indicator）
- **TabContent** — 只掛載 activeTab，切換即銷毀重建（keep-alive 因 tmux resize corruption + WebGL 耗盡移除）
- **StatusBar** — 底部狀態列（host / session / mode）
- **SessionPicker** — Session 選擇 popover（搜尋 + 已開啟標記）
- **useTabStore** — Tab CRUD + `dismissTab` 防止關閉的 tab 被 auto-sync 復活 + localStorage 持久化
- **useWorkspaceStore** — Workspace 管理 + tab 歸屬 + per-workspace activeTab
- **useHostStore** — 取代 hardcoded daemonBase（最小版，Phase 6 擴充為多主機）
- **useUISettingsStore** — 前端 UI 設定（terminalRevealDelay 300ms + terminalRenderer webgl/dom）
- **useIsMobile** — 響應式 breakpoint hook（768px）
- **Hash routing** — `#/tab/{tabId}` 格式，支援 back/forward + 重整後保留
- **App.tsx 重構** — 提取 `useSessionEventWs`、`useSessionTabSync`、`useHashRouting` 三個 custom hooks（345→247 行）
- **xterm.js addons** — `@xterm/addon-unicode11`（CJK 字元寬度）+ `@xterm/addon-web-links`（可點擊 URL）
- **Terminal 渲染器切換** — Settings 新增 WebGL / DOM 下拉選單，變更後自動重連

### 修正

- **crypto.randomUUID fallback** — 非 localhost HTTP context 無法使用，加了 Date.now + Math.random fallback
- **Terminal reveal delay 設定化** — 從 hardcoded 300ms 改為 `useUISettingsStore` 可調整，用 ref + subscribe 避免設定變更觸發 terminal 重建
- **Reconnect overlay 回歸修復** — 恢復 `if (revealed) setReady(true)` 讓 WS 重連後立即顯示 terminal
- **Stale tab 清理** — sessions 消失時自動移除對應 tab（guard `sessions.length > 0` 防止初始渲染清空）
- **Subscribe 洩漏修復** — TerminalView 的 Zustand subscribe 移入 useEffect + cleanup
- **Lint + type errors 全面修正** — 移除 `as any`、修正 SessionStatus type、補 missing fields

### 已知限制

- keep-alive 已移除，每次切 tab 都重新建立 terminal WS 連線（TerminalView 的 `visible` 路徑保留供未來 LRU 快取）
- StatusBar 狀態固定顯示 'connected'（未接 relayStatus/sessionStatus）
- TopBar 標記 @deprecated 但未刪除
- useIsMobile hook 已建立但未在任何元件中使用（Phase 7b）

## [0.5.4] - 2026-03-19

修復 handoff 相關的 terminal resize 與 copy-mode 問題

### 修復

- **Handoff 後 tmux 自動 resize 恢復** — `tmux resize-window -x 80 -y 24`（handoff step 3.5）會讓 tmux 進入手動尺寸模式，導致回到 term 後 window 卡在 80x24 不隨瀏覽器 viewport 調整。handoff 完成 `/status` 擷取後立即呼叫 `resize-window -A` 清除手動旗標
- **Handoff 前退出 tmux copy-mode** — terminal 處於 copy-mode（捲動瀏覽歷史）時 handoff 會失敗。改用 `tmux send-keys -X cancel` 取代依賴 `Escape`，不受 vi/emacs mode 影響且不送按鍵到底層應用

### 新增

- **`[terminal] auto_resize` 設定** — 預設啟用，每次 terminal WS 連線時自動清除手動尺寸旗標。使用者可設 `auto_resize = false` 停用
- **`Executor.ResizeWindowAuto`** — 封裝 `tmux resize-window -A`
- **`Relay.OnStart` callback** — PTY 啟動後的 hook，用於 terminal 連線時重設視窗尺寸

## [0.5.2] - 2026-03-19

架構重構：Stream UI 狀態改由 server-derived relayStatus 驅動

### 重構

- **ConversationView 改用 relayStatus** — 不再依賴 ephemeral `handoffState`，改為 `relayStatus[session]` 作為 single source of truth。Page refresh / WS 重連後自動恢復 stream UI 狀態
- **移除 handoffState** — store 中的 `HandoffState` type、`handoffState` map、`setHandoffState` action 全部移除
- **HandoffButton 簡化** — props 從 `state: HandoffState` 改為 `inProgress: boolean`

### 新增

- **TerminalView `visible` prop** — 切回 term tab 時自動 refit + resize，用遮罩擋住 500ms 等待 tmux 調整完畢再 fadeout

### 修復

- **Handoff 前退出 copy-mode** — 發送 Escape + C-u 退出 tmux 捲動模式並清空輸入，避免 send-keys 注入失敗
- **Handoff Escape error check** — SendKeysRaw(Escape) 失敗時提早返回

## [0.5.1] - 2026-03-18

Bugfix: Handoff tmux target、pane resize、xterm.js 選取

### 修復

- **Handoff tmux target 解析** — 所有 tmux 操作改用 `sess.TmuxTarget`（session:window 格式），避免 bare session name 被 tmux 模糊解析到錯誤的 pane
- **Handoff pane resize** — xterm.js 在 `display:none` 時 PTY 尺寸過小（10x5），tmux smallest-client 規則縮小 pane，`/status` TUI 渲染錯亂。relay PTY 預設 80x24，handoff 前檢查 pane 尺寸並 resize window
- **xterm.js 文字選取** — 啟用 `macOptionClickForcesSelection` 和 `rightClickSelectsWord`，抑制 terminal container 的右鍵選單

## [0.5.0] - 2026-03-18

Stream Message UI — 完整渲染所有 CC 訊息類型

### 新增

- **ThinkingBlock** — 可摺疊的 thinking 區塊（Brain icon，collapsed by default）
- **ToolResultBlock** — 可摺疊的 tool result 顯示（CheckCircle/XCircle 區分成功/錯誤）
- **Slash command 氣泡** — `/exit`、`/status` 等指令以黃棕色氣泡顯示（TerminalWindow bold icon）
- **Interrupted 提示** — 中斷訊息靠左紅棕色顯示（Prohibit icon）
- **@tailwindcss/typography** — 啟用 prose markdown 樣式

### 修改

- **MessageBubble** — User: 藍色氣泡靠右；Assistant: 移除氣泡，直接 prose markdown 輸出（Cowork 風格）
- **ToolCallBlock** — 統一 Wrench icon（移除 per-tool icons），新增 Agent/Grep/Glob summary
- **ConversationView** — 接線所有 content block 類型（thinking、tool_use、tool_result、text、command、interrupted）

### 修復

- **ParseJSONL 過濾 CC 內部標記** — 跳過 `isMeta`、`<local-command-caveat>`、`<local-command-stdout>`、`<synthetic>` assistant；解析 `<command-name>` 為乾淨文字
- **aria-expanded** — 所有可摺疊元件加入無障礙屬性

## [0.4.2] - 2026-03-18

Bugfix: 從 CC `/status` 取得 cwd，修復空 cwd session 的歷史載入

### 新增

- **`detect.ExtractStatusInfo`** — 從 CC `/status` 同時解析 Session ID 和 cwd
- **`store.SessionUpdate.Cwd`** — 支援更新 session 的 cwd 欄位

### 修復

- **空 cwd 導致歷史載入失敗** — auto-scan 使用 `#{session_path}` 取得 cwd，但部分 tmux session 該值為空，導致 history handler 無法定位 JSONL 檔案。改為在 handoff 時從 CC `/status` 輸出取得 cwd 並寫入 DB
- **cwdRegex 空白行誤匹配** — `cwd:` 行僅含空白時不再匹配為有效路徑

## [0.4.1] - 2026-03-18

Bugfix: Handoff 狀態管理修正

### 修復

- **Stream→Term handoff 後 stream 頁面狀態錯誤** — handoff 完成後 `handoffState` 錯留在 `'connected'`，切回 stream tab 時顯示無法互動的對話 UI 而非 HandoffButton。現在根據 session mode 判斷，term handoff 後正確重置為 `'idle'`
- **Term→Stream handoff 載入對話歷史** — `fetchSessions` 改為 await，確保用 fresh session data（含 `cc_session_id`）取得歷史。同時移除 `msgs.length > 0` 條件，空歷史也正確覆蓋避免舊 messages 殘留
- **Relay 關閉時的誤觸事件** — `runHandoffToTerm` 在關閉 relay 前先更新 DB mode 為 `"term"`，防止 `revertModeOnRelayDisconnect` 發送假的 `"failed:relay disconnected"` 事件
- **Handoff 失敗後的 mode rollback** — `runHandoffToTerm` 的 pre-update 在後續步驟失敗時會 rollback mode 到原始值，避免留下不一致的 DB 狀態
- **Term handoff 後清理 per-session state** — 切回 term 時呼叫 `clearSession` 清除上一輪 stream 的 messages、cost、sessionInfo
- **fetchSessions 失敗時的 fallback** — 從 `'connected'`（可能導致無法互動的 UI）改為 `'idle'`（安全預設，顯示 HandoffButton 讓使用者重試）

## [0.4.0] - 2026-03-18

Phase 2.5b: Stream WS Lifecycle Redesign — 修復 stream 訊息不通的根因

### 新增

- **Per-session store** — `useStreamStore` 從全域單例改為 `Record<string, PerSessionState>`，切換 session 不再丟失對話
- **useRelayWsManager hook** — relay 事件驅動 WS 生命週期（relay:connected → 建立 WS，relay:disconnected → 關閉 WS）
- **Relay 事件廣播** — session-events WS 新增 `relay` 事件類型 + snapshot，冷啟動單一資料源
- **Init metadata 攔截** — bridge handler 捕獲 CC init message 的 model 資訊存 DB
- **JSONL history API** — `GET /api/sessions/{id}/history` 讀取 CC 的 JSONL 檔案，resume 時顯示之前的對話
- **SessionResponse DTO** — session list API 回傳 `has_relay` + `cc_model`
- **`cc_model` DB 欄位** — sessions 表新增 cc_model 欄位 + migration
- **`GetSessionByName`** — store 新增 O(1) name 查詢方法
- **`RelaySessionNames`** — bridge 新增列舉所有有 relay 的 session 方法
- **`fetchHistory`** — SPA API client 新增歷史訊息查詢函式

### 修復

- **幽靈連線根因修復** — ConversationView 不再管理 WS 連線，改為純 UI 元件從 per-session store 讀取狀態
- **WS 生命週期脫鉤** — WS 建立/銷毀完全由 relay 事件驅動，不再依賴 component mount 時機
- **set() 內 side effect** — clearSession 的 conn.close() 移到 set() 外避免 re-entrant mutation
- **selector 穩定性** — 使用 stable 空陣列常數避免 Zustand `?? []` 造成的無限 render loop
- **subscribeWithSelector** — store 加入 Zustand middleware 支援 relay status 訂閱

### 改善

- ConversationView props 簡化為 `sessionName`（移除 `wsUrl`、`sessionStatus`）
- session-events type 擴充為 `'status' | 'handoff' | 'relay'`
- bridge 測試恢復 4 個被刪除的單元測試

## [0.3.0] - 2026-03-18

Phase 2.5a: Stream Handoff — 雙向切換

### 新增

- **Stream Handoff** — term（互動式 CC）與 stream（`-p` 串流模式）之間的雙向 handoff
- **SendKeysRaw** — tmux 控制鍵注入（C-u, C-c, Escape 不帶 Enter）
- **ExtractSessionID** — 解析 CC `/status` 輸出的 Session ID（UUID regex）
- **cc_session_id** — sessions 表新欄位 + migration + CRUD
- **Handoff 8 步流程** — CC 偵測 → 中斷 → `/status` 取 ID → `/exit` 退出 → relay `--resume`
- **Handoff to Term** — 6 步反向 handoff（shutdown relay → shell → `claude --resume`）
- **HandoffButton** — CC 狀態感知、進度標籤、disabled 狀態
- **StreamInput "Handoff to Term"** — 底部操作按鈕
- **E2E pipeline 測試** — SPA→bridge→relay→subprocess→bridge→SPA 完整往返驗證
- **Relay 斷線自動 revert** — session mode 自動回 term
- **session-events snapshot** — 新 subscriber 收到初始狀態快照

### 修復

- 混合式 CC 偵測（子程序樹 + pane content fallback）
- relay command 使用 config bind address
- `--verbose` 加入 stream-json preset（CC 2.1.77+ 要求）

## [0.2.0] - 2026-03-17

Phase 2: Stream 模式 — Claude Code 結構化互動

### 新增

- **StreamManager** — `claude -p` 子程序生命週期管理（spawn / stop / pub-sub stdout）
- **WebSocket `/ws/stream/{session}`** — 雙向 NDJSON 中繼（write mutex 保護）
- **Mode Switch API** — `POST /api/sessions/{id}/mode`（term ↔ stream 切換）
- **Store.GetSession** — 單一 session 查詢
- **ConversationView** — 結構化對話渲染（markdown / 程式碼高亮 / 自動捲動）
- **MessageBubble** — user / assistant 訊息氣泡（react-markdown + rehype-highlight）
- **ToolCallBlock** — 可摺疊工具呼叫區塊（工具圖示 + 摘要）
- **PermissionPrompt** — Allow / Deny 按鈕（`can_use_tool` control_request）
- **AskUserQuestion** — radio / checkbox 選項（支援完整 protocol 格式）
- **StreamInput** — 底部訊息輸入框
- **TopBar** — session 名稱 + 三模式按鈕（term / jsonl / stream）+ Stop 按鈕
- **SessionPanel 更新** — Phosphor Icons 狀態燈號 + 底部 Settings 入口
- **useStreamStore** — stream 模式狀態管理（messages / control requests / cost）
- **stream-ws** — 訊息型別定義 + WebSocket 連線管理（含 interrupt / sendControlResponse）

### 修復

- StreamSession：readLoop 中呼叫 cmd.Wait()（防止 zombie process）
- StreamSession：Unsubscribe 關閉 channel（防止 goroutine 洩漏）
- StreamSession：Send 使用 Lock（防止 stdin write race）
- Delete handler：同時停止 stream session（防止子程序洩漏）
- SwitchMode：UpdateSession 錯誤處理 + 回滾
- main.go：st.Close() 改用 defer 保護
- switchMode API：POST 方法（修正 PUT → POST）
- isStreaming 語意：僅在使用者送訊息時啟用（非 WebSocket open）
- AskUserQuestion：回應格式符合 STREAM_JSON_PROTOCOL（含 questions + answers）
- window.__streamConn hack 改為 Zustand store 管理
- clear() 同時重置 sessionId / model
- ConversationView handlers 用 useCallback memoize

### 改善

- TopBar 三模式按鈕（term / jsonl / stream）各自 active 樣式
- TopBar 底色提亮
- Settings 文字亮度對齊 SESSIONS 標題

## [0.1.1] - 2026-03-17

### 修復

- Terminal relay 生命週期：goroutine 互相取消，防止無限 block
- WebSocket write race condition：加入 mutex 保護
- Token 認證改用 constant-time 比較，防止 timing attack
- Token 認證支援 `?token=` query param（WebSocket 無法送 header）
- Session 建立失敗時 rollback tmux session（防止孤立 session）
- Delete handler 正確處理 ListSessions 錯誤
- Batcher 釋放 mutex 後再呼叫 onFlush（防止 deadlock）
- UpdateSession / UpdateGroup 正確回傳錯誤和 ErrNotFound
- Session name 驗證（`^[a-zA-Z0-9_-]+$`）

### 改善

- 自動掃描主機上既有的 tmux sessions（不需手動透過 API 建立）
- Terminal 全寬高顯示（修正 flex layout + 初始 resize 時序）
- WebSocket 連線後送出初始 resize（防止 tmux 按 80x24 渲染）
- URL encode session 名稱（支援空格、中文等特殊字元）
- Loading overlay 帶呼吸動畫，收到資料後 300ms fade out
- Session 按鈕加 cursor pointer + 切換後自動 focus terminal
- Sidebar 文字亮度提升
- Zustand persist 只存 activeId（避免快取過期 session 資料）
- WebSocket onmessage 加 ArrayBuffer type guard
- ResizeObserver 用 requestAnimationFrame debounce
- ws.ts 修正 onerror + onclose 重複觸發 onClose

## [0.1.0] - 2026-03-17

Phase 1: Daemon 基礎 + Terminal 模式

### 新增

- **tbox daemon** — Go HTTP + WebSocket API server
  - Config 載入（TOML，自動讀取 `~/.config/tbox/config.toml`）
  - SQLite 持久化（sessions / groups CRUD）
  - tmux session 管理（建立 / 刪除 / 列出）
  - Terminal relay（WebSocket ↔ PTY 雙向中繼，含 resize）
  - DataBatcher（16ms / 64KB 輸出批次化）
  - 安全：IP 白名單（CIDR）、token 認證（constant-time 比較）、CORS
  - Session name 驗證（`^[a-zA-Z0-9_-]+$`）
  - Graceful shutdown（SIGTERM / SIGINT）

- **tbox spa** — React SPA（獨立部署）
  - Session 面板（左側選單，模式圖示，active 高亮）
  - Terminal 畫面（xterm.js + WebGL + FitAddon + resize）
  - API client（可設定 daemon base URL）
  - Session store（Zustand + localStorage 持久化）

### 架構

- Daemon 和 SPA 完全分離部署
- Daemon 是純 API server，不含前端檔案
- SPA 可封裝為 Electron 或放在獨立主機上 serve

### 技術棧

- Daemon: Go / net/http / gorilla/websocket / creack/pty / modernc.org/sqlite / BurntSushi/toml
- SPA: React 19 / Vite / xterm.js / Zustand / Tailwind CSS / Vitest
