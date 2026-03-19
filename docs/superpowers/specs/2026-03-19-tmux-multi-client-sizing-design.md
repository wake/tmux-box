# tmux 多 Client 視窗尺寸修正設計

> 日期：2026-03-19
> 版本：v0.6.0
> 狀態：設計確認

---

## 問題

### Root Cause

`tmux resize-window -A` 有未記載的副作用 — 自動將 `window-size` 設為 `manual`。一旦變成 `manual`，tmux 不再理會任何 client 的尺寸變化。

影響發生在所有呼叫 `resize-window` 的地方：

| 位置 | 呼叫 | 副作用 |
|------|------|--------|
| `server.go` relay OnStart | `ResizeWindowAuto(name)` | 設為 manual |
| `handoff_handler.go` 前置 | `ResizeWindow(target, 80, 24)` | 設為 manual |
| `handoff_handler.go` 後置 | `ResizeWindowAuto(target)` | 設為 manual |

### 症狀

1. 單一瀏覽器 refresh 後，iTerm 的尺寸被鎖死
2. 第二個瀏覽器（較大）連線後，第一個瀏覽器和 iTerm 的畫面溢出
3. 調整瀏覽器視窗大小無法恢復（因為 `window-size` 已被鎖為 `manual`）

### 多 Client 互搶

即使修復 `manual` 副作用，多個 client 在 `window-size latest` 策略下仍會互相影響 — 最近有活動的 client 決定所有 client 的 window 尺寸。

---

## 方案

採用 **方案 C（A + B 組合）**：

1. **Part 1**：修復 `resize-window -A` 副作用（必要 bug fix）
2. **Part 2**：Session Group 可選配置（進階功能）

---

## Part 1：修復 resize-window -A 副作用

### 變更

在 `tmux.Executor` 介面新增方法：

```go
SetWindowOption(target, option, value string) error
```

實作：

```go
func (r *RealExecutor) SetWindowOption(target, option, value string) error {
    return exec.Command("tmux", "set-window-option", "-t", target, option, value).Run()
}
```

### 修復點

每次 `resize-window` 之後恢復 `window-size latest`：

**server.go relay OnStart**（relay 連線後）：

```go
s.tmux.ResizeWindowAuto(name)
s.tmux.SetWindowOption(name, "window-size", "latest")
```

**handoff_handler.go 後置清理**（handoff 結束後）：

```go
s.tmux.ResizeWindowAuto(target)
s.tmux.SetWindowOption(target, "window-size", "latest")
```

**handoff_handler.go 前置**（handoff 期間 `ResizeWindow(80,24)`）：不恢復。handoff 期間刻意固定尺寸，由後置清理統一恢復。

### 不包在 ResizeWindowAuto 內部的原因

handoff 中間的 `ResizeWindow(80,24)` 是刻意要暫時固定尺寸（確保 `/status` TUI 可正常顯示），到後置清理才恢復。如果把恢復邏輯包在 `ResizeWindowAuto` 裡，會與 `ResizeWindow` 的行為不一致。呼叫端明確控制何時恢復更清晰。

---

## Part 2：Session Group 可選配置

### 配置

```toml
[terminal]
auto_resize = true        # 已有，預設 true
session_group = false      # 新增，預設 false
```

```go
type TerminalConfig struct {
    AutoResize   *bool `toml:"auto_resize"   json:"auto_resize"`
    SessionGroup *bool `toml:"session_group"  json:"session_group"`
}

func (tc TerminalConfig) IsSessionGroup() bool {
    return tc.SessionGroup != nil && *tc.SessionGroup
}
```

預設 `false`，因為 session group 改變了 tmux 行為（各 session 的 current window 指標獨立，切 window 不再連動），需要使用者主動選擇。

### relay 建立方式

`handleTerminal` 根據配置選擇 attach 方式：

```go
// session_group = false（現行）
tmux attach-session -t {name}

// session_group = true
relaySession := fmt.Sprintf("%s-tbox-%s", name, shortID())
tmux new-session -d -t {name} -s {relaySession}
tmux attach-session -t {relaySession}
```

用 `-d`（detached）+ `attach` 兩步，因為 `new-session -t` 直接執行會嘗試接管當前終端，在 PTY 環境下需要分開處理。

### 清理

**正常斷開**：relay 的 PTY close / WS disconnect 時，kill grouped session：

```go
defer exec.Command("tmux", "kill-session", "-t", relaySession).Run()
```

**異常殘留清理**：daemon 啟動時清理 `*-tbox-*` pattern 的 sessions（異常退出可能留下）：

```go
// server.go resetStaleModes() 中
sessions := tmux list-sessions -F '#{session_name}'
for each session matching "*-tbox-*":
    tmux kill-session -t {session}
```

### 與 auto_resize 的交互

Session group 啟用時，`resize-window -A` 和 `SetWindowOption` 作用在 grouped session 的 window 上。`auto_resize` 的目標從 `name`（原始 session）改為 `relaySession`（grouped session），只影響該 relay 自己。

### 與 handoff 的交互

Handoff 操作的是原始 session（`name`），不是 grouped session。handoff 期間的 `ResizeWindow` 和 `ResizeWindowAuto` 仍然作用在原始 session 上，不受 session group 影響。

---

## 測試計畫

### Part 1 測試

- 驗證 `ResizeWindowAuto` 後 `window-size` 恢復為 `latest`
- 驗證 `ResizeWindow(80,24)` 後 `window-size` 為 `manual`（handoff 期間刻意行為）
- 驗證 handoff 後置清理恢復 `window-size latest`
- `FakeExecutor` 新增 `SetWindowOption` 記錄

### Part 2 測試

- `IsSessionGroup()` 預設 false、設為 true 時返回 true
- session_group=true 時 relay 建立 grouped session
- relay 斷開時 grouped session 被 kill
- daemon 啟動時清理殘留 `*-tbox-*` sessions
- session_group=false 時行為與現行一致
