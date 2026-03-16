# Changelog

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
