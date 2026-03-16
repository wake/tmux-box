# tmux-box 設計規格

## 概述

tmux-box 是一套以 SPA 為核心的 tmux session 管理與 Claude Code 互動工具。每台主機運行一個 Go daemon，提供 HTTP + WebSocket API；前端以 React SPA 實作，可透過 Electron 封裝為桌面應用或直接用瀏覽器（含手機）存取。

**核心目標**：提供比 terminal 更好的 Claude Code 互動體驗，同時保留隨時 SSH attach 接手的能力。

---

## 架構

### 系統拓撲

```
┌─ Client ──────────────────────────────────────────────┐
│  Electron (桌機) 或 Browser (手機)                      │
│  React SPA                                             │
│  └── 連接 N 台主機的 daemon，聚合所有 session            │
└──── HTTP / WebSocket ──────────────────────────────────┘
         │              │              │
    ┌────▼────┐    ┌────▼────┐    ┌────▼────┐
    │ Host A  │    │ Host B  │    │ Host C  │
    │ daemon  │    │ daemon  │    │ daemon  │
    │ (Go)    │    │ (Go)    │    │ (Go)    │
    └────┬────┘    └────┬────┘    └────┬────┘
         │              │              │
    tmux + claude  tmux + claude  tmux + claude
```

- 每台主機獨立運行一個 daemon binary
- 每個 daemon 有自己的 SQLite、設定檔
- Client 連接多個 daemon，在 UI 中聚合顯示
- 加主機 = 部署一個 daemon binary + 設定

### Daemon 元件

```
Go daemon
├── HTTP Server
│   ├── 靜態檔（go:embed SPA build）
│   ├── REST API
│   │   ├── GET    /api/sessions         — 列出 sessions
│   │   ├── POST   /api/sessions         — 建立 session
│   │   ├── DELETE /api/sessions/:id     — 刪除 session
│   │   ├── PUT    /api/sessions/:id     — 更新 metadata
│   │   ├── GET    /api/groups           — 列出群組
│   │   ├── POST   /api/groups           — 建立群組
│   │   ├── PUT    /api/groups/:id       — 更新群組
│   │   ├── GET    /api/files?path=      — 列出目錄
│   │   ├── GET    /api/file?path=       — 讀取檔案
│   │   ├── PUT    /api/file?path=       — 寫入檔案
│   │   └── GET    /api/config           — daemon 設定
│   │
│   └── WebSocket Endpoints
│       ├── /ws/terminal/:session   — PTY 資料雙向中繼
│       ├── /ws/stream/:session     — CC stream-json 雙向中繼
│       ├── /ws/journal/:session    — JSONL 變更推送（單向）
│       └── /ws/status              — 全域狀態更新（單向）
│
├── SessionManager
│   ├── tmux session/window/pane CRUD（透過 tmux CLI）
│   ├── 定期掃描 tmux 狀態
│   └── session metadata（SQLite）
│
├── TerminalRelay
│   ├── 透過 creack/pty spawn `tmux attach-session -t {target}`
│   ├── WebSocket ↔ PTY 雙向資料轉發
│   ├── DataBatcher（先到先觸發：每 16ms 或累積 64KB 即送出一批）
│   ├── 多 client 語義：多人同時 attach 同一 session（唯讀 + 一個 active controller）
│   └── 連線中斷時暫存最近 4096 行，重連後推送
│
├── StreamManager
│   ├── claude -p 子程序生命週期管理
│   ├── spawn 時指定 cwd（工作目錄）
│   ├── stdin/stdout JSON 雙向中繼到 WebSocket
│   ├── control_request/control_response 轉發（權限、AskUserQuestion）
│   └── 程序異常退出 → 保留 session ID 供 resume
│
├── JournalWatcher
│   ├── fsnotify 監聽 ~/.claude/projects/.../*.jsonl
│   ├── 增量推送新行到 WebSocket client
│   └── session → JSONL 映射：
│       路徑格式 ~/.claude/projects/{encoded-cwd}/{uuid}.jsonl
│       encoded-cwd = cwd 中 / 替換為 -
│       daemon 以 session cwd 推導目錄，按修改時間找最近的 active 檔案
│
├── FileService
│   ├── 目錄列舉（含 .gitignore 過濾）
│   ├── 檔案讀取（文字 + 二進制偵測）
│   └── 檔案寫入（原子寫入 + 備份）
│
└── SQLite
    ├── sessions（name, tmux_target, cwd, mode, group_id, sort_order）
    ├── groups（name, sort_order, collapsed）
    └── hosts_config（local daemon 設定）
```

### Daemon 設定

```toml
# ~/.config/tbox/config.toml

bind = "100.64.0.2"
port = 7860

# 連線白名單（支援 CIDR）
allow = [
  "100.64.0.0/24",   # Tailnet
  "127.0.0.1",
]

# Pre-shared key 認證（WebSocket upgrade 和 API 請求皆需帶此 token）
# 留空則僅依賴 IP 白名單
token = ""

data_dir = "~/.config/tbox"

# File API 允許存取的根目錄（限制在這些路徑下）
# 空陣列 = 僅允許各 session 的 cwd 及其子目錄
allowed_paths = []
```

### 安全模型

- **網路層**：Tailscale 網路隔離 + IP 白名單（CIDR）
- **應用層**：`tbox auth create` 產生金鑰組；CORS 全開（安全由網路層 + 金鑰處理）
- **File API 沙盒**：每個 session 的檔案操作限制在其 cwd 及子目錄下；`allowed_paths` 可額外開放指定目錄；拒絕 symlink 逃逸
- **前提假設**：部署在受信任的 Tailscale 網路內，單一使用者場景

### Host 管理

- Host 清單存放在 **client 端**（Electron: localStorage / 瀏覽器: localStorage）
- 每筆 host 記錄：`{ name, address, port, token?, color, enabled }`
- 新增 host：輸入 address + port + token → SPA 嘗試連線 → 成功則儲存
- Host 離線時：Session Panel 中該 host 顯示為灰色，session 列表凍結在最後已知狀態，標示「離線」
- Host 之間無直接通訊，所有聚合由 client 負責

---

## Session 三模式

每個 session 有三種操作模式，可在 UI 中切換。

### Terminal 模式（term）

- **tmux 內容**：任何程式（shell、claude TUI、htop 等）
- **前端渲染**：xterm.js 直接渲染 PTY 輸出
- **輸入**：WebSocket 直送 PTY（鍵盤事件即時轉發）
- **適用場景**：一般 shell 操作、非 Claude Code 工作
- **daemon 角色**：TerminalRelay 雙向中繼 WebSocket ↔ PTY

### JSONL 模式（jsonl）

- **tmux 內容**：claude（TUI 互動模式）
- **前端渲染**：結構化 web 渲染（message 級即時性）
- **輸入**：tmux send-keys 注入使用者訊息
- **適用場景**：CC 工作 + 需保留 SSH attach 能力
- **daemon 角色**：JournalWatcher 監聯 JSONL + SessionManager 執行 send-keys
- **send-keys 注入協定**：
  - 送出前先發 `Escape` 確保 CC 處於正常輸入狀態
  - 訊息內容透過 `tmux load-buffer` + `tmux paste-buffer` 注入（避免特殊字元問題）
  - 最後發 `Enter` 提交
  - 權限回應（y/n）直接 send-keys 單一字元

### Stream 模式（stream）

- **tmux 內容**：tmux session 預留（空 pane 或 wrapper）
- **前端渲染**：結構化 web 渲染（token 級即時性）
- **輸入**：stdin JSON 直送 claude -p 子程序
- **適用場景**：CC 工作 + 最佳 web 體驗
- **daemon 角色**：StreamManager 管理子程序生命週期 + 雙向 JSON 中繼

### 模式切換規則

- **term ↔ jsonl**：即時切換（同一個 CC TUI 實例的兩種「視角」，不影響 tmux）
- **term/jsonl → stream**：
  1. 前端提示使用者在 tmux 中結束 CC（或 daemon 發 `tmux send-keys /exit Enter`）
  2. 等待 CC 程序結束
  3. daemon 取得 CC session ID（從 JSONL 目錄中最近的檔案）
  4. daemon spawn `claude -p --resume <session-id> --input-format stream-json --output-format stream-json` 並指定 cwd
- **stream → term/jsonl**：
  1. daemon 優雅結束 `claude -p` 子程序（SIGTERM）
  2. daemon 在 tmux pane 中執行 `tmux send-keys "claude --resume <session-id>" Enter`
  3. 前端切換到 term 或 jsonl 視角
- **非 CC 工作**：只能在 term 模式下操作

### Stream 模式 control protocol

daemon 轉發 `claude -p` 的 control_request 到前端 WebSocket，前端渲染對應 UI：

| control_request 類型 | 前端 UI |
|---------------------|---------|
| `can_use_tool`（一般工具） | Allow / Deny 按鈕 + 工具描述 |
| `can_use_tool`（AskUserQuestion） | 選單元件（radio/checkbox） |

前端收集使用者回應後，組成 control_response 透過 WebSocket 回傳 daemon，daemon 寫入 `claude -p` 的 stdin。超時不處理 — `claude -p` 會自行等待。

---

## UI 設計

### 佈局

三欄式佈局：

```
┌──────────┬────────────────────────────┬──────────┐
│ Session  │ 主內容區                    │ File     │
│ Panel    │                            │ Explorer │
│          │ ┌──────────────────────┐   │          │
│ ● host A │ │ Tab Bar              │   │ ▾ cmd/   │
│   group1 │ │ [session1] [file.md] │   │   main.go│
│    sess1 │ ├──────────────────────┤   │ ▾ internal│
│    sess2 │ │                      │   │   ...    │
│ ● host B │ │  Content Area        │   │ CLAUDE.md│
│    sess3 │ │  (term/conv/editor)  │   │          │
│          │ │                      │   │          │
│ + New    │ ├──────────────────────┤   │          │
│          │ │ Input Area           │   │          │
└──────────┴┴──────────────────────────┴──────────┘
```

### Session Panel（左側）

- 按主機分組顯示所有已連線 session
- 每個 session 顯示：名稱、AI 狀態圖示、當前模式（term/jsonl/stream）
- 支援群組（可展開/收合）
- 底部「+ New Session」按鈕
- 拖拽排序、移動到群組

### 主內容區（中間）

- **Tab Bar**：session 分頁 + 檔案編輯分頁，混合排列
- **模式切換**：每個 session tab 右上角可切 term / web（jsonl 或 stream）
- **Terminal 模式**：xterm.js 全區域渲染
- **Web 模式（jsonl/stream）**：
  - 對話歷史：結構化渲染（markdown、程式碼高亮、tool call 摺疊、diff 顯示）
  - 權限提示：Allow/Deny 按鈕
  - AskUserQuestion：原生選單元件
  - 底部輸入區：文字輸入 + 送出
- **檔案編輯**：Monaco editor，支援多分頁

### File Explorer（右側）

- 顯示當前 active session 的工作目錄檔案樹
- 點擊檔案 → 在主內容區開新分頁（Monaco editor）
- 支援建立 / 刪除 / 重新命名（後續版本）
- .gitignore 過濾

---

## New Session 流程

1. 選擇目標主機（從已連線的 daemon 中選）
2. 輸入 session 名稱
3. 指定工作目錄（路徑輸入 + 歷史記錄選單）
4. 選擇初始模式：term / stream
5. 可選：指定群組
6. Daemon 執行：
   - `tmux new-session -d -s {name} -c {cwd}`
   - 如果 stream 模式：spawn `claude -p --input-format stream-json --output-format stream-json --verbose --include-partial-messages` 並指定 cwd

---

## 斷線與重連

### Terminal / JSONL 模式

- tmux 天然處理 — CC 繼續在 tmux 中執行
- 網路恢復後 WebSocket 重連
- TerminalRelay 可暫存最近 N 行輸出，重連後推送
- JournalWatcher 重連後從斷點繼續推送 JSONL 行

### Stream 模式

- `claude -p` 由 daemon 管理（不是 Electron spawn）
- 斷線時子程序繼續在 daemon 上執行
- 重連後 daemon 推送累積的 JSON 事件
- 子程序異常退出 → daemon 保留 session ID，前端可觸發 `--resume` 重啟

---

## 資料獨立性

tmux-box 擁有自己的資料層，不依賴 tsm 安裝：

- **SQLite**：`~/.config/tbox/state.db`
- **設定**：`~/.config/tbox/config.toml`
- **與 tsm 的關係**：後續版本可選擇性橋接 tsm 的 SQLite（共享群組/metadata），但初始版本完全獨立

---

## 技術棧

### Daemon（Go）

| 領域 | 選擇 |
|------|------|
| HTTP | net/http 或 chi router |
| WebSocket | gorilla/websocket 或 nhooyr.io/websocket |
| PTY | github.com/creack/pty |
| SQLite | modernc.org/sqlite（純 Go，免 CGO） |
| 檔案監聽 | github.com/fsnotify/fsnotify |
| 靜態檔嵌入 | go:embed |
| 設定 | github.com/BurntSushi/toml |

### SPA（React + TypeScript）

| 領域 | 選擇 |
|------|------|
| 框架 | React 19 |
| 建構 | Vite |
| 終端 | xterm.js + WebGL addon + fit addon |
| 編輯器 | Monaco Editor (@monaco-editor/react) |
| 狀態管理 | Zustand 或 Jotai |
| WebSocket | 原生 WebSocket + reconnecting-websocket |
| 樣式 | Tailwind CSS |
| Markdown | react-markdown + rehype-highlight |

### 架構分離

Daemon 和 SPA 完全分離部署：
- **tbox daemon**：純 API server（REST + WebSocket），不含任何前端檔案
- **tbox spa**：獨立 React app，可封裝為 Electron（桌機）或放在獨立主機上 serve（手機+桌機）

### 桌面封裝

| 領域 | 選擇 |
|------|------|
| 框架 | Electron |
| 附加功能 | 系統通知、全域快捷鍵、Dock/Tray icon |
| SPA 來源 | 連接遠端 daemon（非本機打包） |

---

## 部署

### Daemon 部署

```bash
# 編譯
make build    # → bin/tbox

# 部署到遠端
scp bin/tbox user@host:~/.local/bin/

# 產生認證金鑰
tbox auth create --name "my-macbook"

# 啟動
tbox daemon
# 或
tbox daemon --bind 100.64.0.2 --port 7860
```

### SPA 部署

```bash
cd spa/
pnpm build    # → dist/

# 選項 A: Electron 封裝
pnpm electron:build

# 選項 B: 靜態檔部署到任意 web server
rsync -av dist/ server:/var/www/tbox/
```

---

## 錯誤處理

- **tmux server 未啟動**：daemon 啟動時偵測，嘗試 `tmux start-server`；失敗則 log 警告並持續重試
- **claude CLI 未安裝**：建立 stream session 時回傳錯誤，前端提示使用者安裝；term/jsonl 模式不受影響
- **JSONL 檔案被刪除/rotate**：JournalWatcher 偵測到刪除事件後停止推送，前端顯示「session 記錄已結束」
- **SQLite 與 tmux 狀態不一致**：SessionManager 定期掃描 tmux，同步刪除已不存在的 session 記錄
- **Daemon graceful shutdown**：SIGTERM → 優雅結束所有 stream 子程序 → 關閉 WebSocket 連線 → 關閉 HTTP server

## 不在初始範圍內

- tsm 資料橋接（後續版本）
- 使用者認證系統（初始依賴 IP 白名單 + Tailscale + 可選 token）
- 多使用者協作
- 檔案建立 / 刪除 / 重新命名（初始版本只有讀取和編輯）
- 自動升級機制
- PWA / Service Worker 離線支援
- API 版本控制（初始版本為單一版本，版本不符時前端提示升級）
- daemon 的 systemd / launchd 服務配置（文件後補）
