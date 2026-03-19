# tmux-box 分頁 + 工作區 UI 重構設計

**日期**: 2026-03-20
**狀態**: Draft
**範圍**: SPA 前端 UI 架構重構，後端 API 擴充

---

## 1. 設計目標

將 tmux-box 從「單 session 檢視」升級為「多分頁 + 工作區 + 多主機」架構，同時保持 terminal 內容最大化的核心體驗。

### 核心原則

- 內容區域最大化，所有面板可折疊
- 工作區即分頁群組，不另設獨立群組概念
- Activity Bar 統一處理工作區/獨立分頁切換
- 側欄 4 區域自由配置 + 固定/預設/縮減三模式
- 圖示統一使用 Phosphor Icons
- 紫色（#7a6aaa 系列）為 UI 重點色系

---

## 2. 整體佈局

**選定方案：Activity Bar + 分頁列 + 4 區域側欄**

```
┌──────┬──────┬──────────────────────────────┬──────┐
│      │ 左   │ 分頁列                        │ 右   │
│ Act  │ tab  │ [tab][tab][tab]            +  │ tab  │
│ Bar  │ 外   ├──────┬───────────────┬──────┤ 外   │
│      │      │ 左   │               │ 右   │      │
│[WS1] │      │ tab  │   內容區域     │ tab  │      │
│[WS2] │      │ 內   │  Terminal /   │ 內   │      │
│[WS3] │      │      │  Stream /     │      │      │
│      │      │      │  Editor       │      │      │
│ [+]  │      │      │               │      │      │
│ [⚙] │      │      │               │      │      │
├──────┴──────┴──────┴───────────────┴──────┴──────┤
│  host │ session │ status              │ mode │ sz │  ← 狀態列
└──────────────────────────────────────────────────────┘
```

### 佈局元素

| 區域 | 說明 | 可見性 |
|------|------|--------|
| Activity Bar | 工作區/獨立分頁切換（類 Slack） | 常駐，最左側 |
| 左 tab 外 | 系統級面板區（全高） | 可折疊/縮減/固定 |
| 分頁列 | 當前工作區的分頁 + 群組操作 | 常駐 |
| 左 tab 內 | 工作區級面板區（tab 下方） | 可折疊/縮減/固定 |
| 內容區域 | 當前分頁的內容渲染 | 常駐 |
| 右 tab 內 | 工作區級面板區（tab 下方） | 可折疊/縮減/固定 |
| 右 tab 外 | 系統級面板區（全高） | 可折疊/縮減/固定 |
| 狀態列 | 當前連線狀態摘要 | 常駐 |

---

## 3. 分頁系統

### 3.1 分頁類型

| 類型 | 圖示 | 說明 |
|------|------|------|
| Terminal | `Terminal` | tmux session 終端連線（xterm.js） |
| Stream | `ChatCircleDots` | Claude Code 串流對話 |
| Editor | `File` / `FileCode` | 遠端檔案檢視/編修 |
| （預留） | — | 未來擴充用 |

### 3.2 分頁狀態

每個分頁為一個獨立實體，包含：

```typescript
interface Tab {
  id: string              // 唯一識別
  type: 'terminal' | 'stream' | 'editor'
  label: string           // 顯示名稱
  icon: string            // Phosphor icon name
  hostId: string          // 所屬主機
  sessionName?: string    // terminal/stream: tmux session 名稱
  filePath?: string       // editor: 遠端檔案路徑
  isDirty?: boolean       // editor: 是否有未儲存變更
}
```

**所有權原則：** Tab 不持有 `workspaceId`。分頁與工作區的歸屬關係由 `Workspace.tabs` 單向管理（Workspace 為 source of truth）。判斷一個分頁是否為獨立分頁，透過「不被任何 Workspace.tabs 包含」來推導。這避免雙向引用造成的同步問題。

### 3.3 分頁操作

- **點擊**: 切換到該分頁
- **中鍵點擊 / 關閉按鈕**: 關閉分頁
- **拖曳**: 重新排序，或拖入/拖出工作區
- **右鍵選單**: 關閉、關閉其他、移到工作區、複製路徑等

---

## 4. 工作區（Workspace）

工作區本質上是**帶有上下文的分頁群組**。

### 4.1 工作區結構

```typescript
interface Workspace {
  id: string
  name: string
  color: string            // 群組 chip 的色標
  directories: PinnedItem[] // 釘選的目錄和檔案
  tabs: string[]           // tab IDs（有序）
  activeTabId: string      // 當前活躍分頁
  sidebarState: WorkspaceSidebarState // 側欄面板狀態（按工作區記憶）
}

// 每個工作區記憶的側欄狀態
interface WorkspaceSidebarState {
  // 每個區域記住：當前選中的面板、寬度、模式
  zones: Record<SidebarZone, {
    activePanelId?: string  // 當前選中面板
    width: number           // 面板寬度（px）
    mode: 'fixed' | 'default' | 'collapsed'
  }>
}

type SidebarZone = 'left-outer' | 'left-inner' | 'right-inner' | 'right-outer'

interface PinnedItem {
  type: 'directory' | 'file'
  hostId: string
  path: string
}
```

### 4.2 工作區功能

- **跨主機**: 一個工作區可以混合不同 host 的 sessions、目錄
- **目錄監看**: 釘選的目錄/檔案即時顯示變動（新增、修改、刪除標記）
- **上下文切換**: 切換工作區時，側欄目錄/Git/資訊面板跟著切換
- **啟動 Session**: 從工作區直接建立新的 tmux session
- **側欄記憶**: 每個工作區記住自己的側欄面板選擇和寬度

### 4.3 獨立分頁

不屬於任何工作區的分頁：
- 在分頁列上與工作區群組並列（用分隔線區隔）
- 仍可在側欄顯示自己的相關資訊（如 terminal 的 session 狀態）
- 可拖入工作區加入群組

---

## 5. Activity Bar

Activity Bar 取代先前的 Bar 下拉切換系統，以垂直圖示列統一處理工作區和獨立分頁的切換，類似 Slack 的工作區切換體驗。

### 5.1 位置與外觀

- 固定在畫面最左側，全高
- 垂直排列：工作區圖示 → 分隔線 → 獨立分頁圖示 → 分隔線 → 新增/設定
- 當前活躍工作區以高亮邊框標示
- 工作區圖示可自訂（emoji 或 Phosphor icon），背景色使用工作區色標

### 5.2 Activity Bar 項目

```typescript
// Activity Bar 顯示的項目由 Workspace 和獨立 Tab 組合而成
// 不需要獨立的 Bar store，直接由 WorkspaceStore + TabStore 推導
```

| 區塊 | 內容 | 操作 |
|------|------|------|
| 工作區區 | 每個 Workspace 一個圖示 | 點擊切換，右鍵選單管理 |
| 獨立分頁區 | 不屬於任何工作區的 Tab | 點擊切換到該分頁 |
| 操作區 | + 新增、⚙ 設定 | 新增工作區/分頁、開啟設定 |

### 5.3 行為

- **點擊工作區** → 切換到該工作區（tab bar 顯示其分頁，側欄切換 context）
- **點擊獨立分頁** → 切換到該分頁
- **拖曳** → 重新排序工作區/獨立分頁
- **右鍵工作區** → 重新命名、變更圖示/顏色、刪除等

---

## 6. 橫列式群組行為

### 6.1 展開/收合的控制方式

每個工作區群組**各自獨立**控制展開/收合狀態（不是全域開關）。

- **預設狀態**：所有群組展開（平攤子分頁）
- **切換方式**：點擊群組 chip 左側的展收箭頭（或雙擊群組 chip）
- **記憶**：每個群組的展收狀態持久化

### 6.2 展開狀態

工作區的子分頁平攤在分頁列中，操作方式與一般分頁相同。群組 chip 在最前方，帶有工作區色標。分頁列維持**單行**。

```
│ ⚑[色標] WS名稱 │ [tab1] [tab2] [tab3] │ ... │
```

### 6.3 收合狀態（子母層）

當**任何一個以上的群組收合**時，分頁列轉為**雙行結構**：

- **母層（上）**：收合的群組 chip（顯示分頁數量）+ 展開的群組 chip（不含子分頁）+ 獨立分頁
- **子層（下）**：目前被選中的收合群組的子分頁列表

點擊不同的收合群組 chip → 切換子層顯示該群組的分頁。

```
母層: │ ⚑ WS1(3) │ ⚑ WS2(2) │ [standalone] │ + │
子層: │ [tab1]  [tab2]  [tab3]                     │  ← WS1 的分頁
```

**當所有群組都展開時**，子層消失，回到單行結構。

### 6.4 混合狀態

部分群組展開、部分收合時：
- 展開的群組子分頁在母層平攤（和一般分頁混在一起）
- 收合的群組只在母層顯示 chip
- 子層顯示當前被選中的收合群組的分頁

### 6.5 群組操作

- **點擊群組 chip**: 展開模式下選中工作區；收合模式下切換子層內容
- **展收箭頭 / 雙擊**: toggle 群組展開/收合
- **右鍵群組 chip**: 重新命名、變更顏色、展開/收合、刪除工作區等
- **拖曳分頁進群組**: 加入該工作區
- **拖曳分頁出群組**: 變為獨立分頁

---

## 7. 側欄面板

### 7.1 六種面板

面板分為兩個層級：

**系統級面板（預設放 tab 外位置）：**

| # | 面板 | Phosphor Icon | 說明 |
|---|------|---------------|------|
| 1 | Sessions | `List` | 所有 host 的 tmux session 清單，按主機分組 |
| 2 | 提示詞 | `Lightning` | 可編輯的提示詞清單，點擊注入當前分頁輸入 |

**工作區級面板（預設放 tab 內位置）：**

| # | 面板 | Phosphor Icon | 說明 |
|---|------|---------------|------|
| 3 | 目錄 | `FolderOpen` | 當前工作區的釘選目錄/檔案，即時變動標記 |
| 4 | Git | `GitBranch` | 當前工作區目錄的 git log / changes / branches |
| 5 | 資訊 | `Info` | 工作區摘要（host、branch、分頁數、sessions） |
| 6 | AI 歷史 | `ClockCounterClockwise` | 工作區的 stream 對話歷史紀錄 |

### 7.2 四區域配置

面板可放置在 4 個側欄區域，使用者自由拖曳配置：

| 區域 | 垂直範圍 | 預設層級 | 說明 |
|------|----------|----------|------|
| 左 tab 外 | 全高（與 tab bar 並列） | 系統級 | Activity Bar 右側 |
| 左 tab 內 | tab bar 下方 | 工作區級 | 內容區左側 |
| 右 tab 內 | tab bar 下方 | 工作區級 | 內容區右側 |
| 右 tab 外 | 全高（與 tab bar 並列） | 系統級 | 最右側 |

每個區域可放置一或多個面板，以圖示分頁切換。配置透過拖曳或設定面板調整（後期實作）。

### 7.3 三種面板模式

每個區域的面板有三種顯示模式，可快捷鍵循環切換：

| 模式 | 行為 | 觸發 |
|------|------|------|
| **📌 固定** | 始終展開顯示，不受 context 切換影響 | 手動釘選 |
| **⚡ 預設** | 同側 tab 外+內 智慧切換（見 7.4） | 預設行為 |
| **◁ 縮減** | 收窄為垂直按鈕條，hover / 快捷鍵浮動展開，離開焦點自動收回 | 手動設定 / 預設自動 |

### 7.4 同側智慧切換（預設模式行為）

同一側的 tab 外和 tab 內面板在預設模式下，根據 context 自動切換優先級：

**在工作區分頁時：**
- tab 內面板（目錄、Git 等）→ **展開**（優先）
- tab 外面板（Sessions 等）→ **縮減**

**在獨立分頁時：**
- tab 外面板（Sessions 等）→ **展開**（優先）
- tab 內面板 → **縮減**（或隱藏，無工作區 context）

📌 固定模式的面板不參與自動切換，始終保持展開。

### 7.5 縮減模式細節

- 縮減為窄條（約 24px），顯示垂直文字標籤和圖示
- **Hover**：浮動展開面板覆蓋在內容區上方（不推擠佈局）
- **快捷鍵**：展開面板，再次按下或焦點離開時收回
- 類似 Visual Studio 的 auto-hide dock 行為

### 7.6 面板操作

- 區域內圖示列切換面板內容
- 拖曳邊緣調整寬度
- 拖到最小自動進入縮減
- 快捷鍵 toggle 模式（預設 ↔ 縮減 ↔ 固定）

### 7.7 上下文感知

- 切換工作區（Activity Bar）→ tab 內的目錄/Git/資訊/AI 歷史面板跟著切換
- 選中獨立分頁 → tab 內面板縮減，tab 外面板展開
- 每個工作區記憶自己的側欄面板選擇和寬度

### 7.8 Sessions 面板細節

- 按主機分組顯示所有 session
- 顯示 session 名稱、模式圖示、狀態（running/streaming/idle）
- 點擊 session → 開啟為新分頁（或切換到已開啟的分頁）
- 支援搜尋過濾
- 底部「新增 Session」按鈕

### 7.9 目錄面板細節

- 顯示當前工作區的釘選目錄和檔案
- 樹狀展開目錄結構
- 即時顯示檔案變動標記（M: modified, +: added, D: deleted, 數字: 變動數量）
- 點擊檔案 → 開啟為 Editor 分頁
- 底部「釘選」按鈕新增目錄或檔案
- 核心用途之一：監看 AI 工作過程中的檔案變動

### 7.10 提示詞注入細節

- 可編輯維護的提示詞清單
- 點擊提示詞 → 依當前分頁類型注入：
  - Terminal 分頁 → 送入 tmux session
  - Stream 分頁 → 貼入 StreamInput 輸入框
  - Editor 分頁 → 不適用（或插入游標位置）
- 支援變數模板（如 `{dir}` 替換為當前目錄）

---

## 8. Quick Switcher

### 8.1 觸發方式

- 快捷鍵 ⌘K（可自訂）
- 側欄 Sessions 面板中的搜尋也可觸發

### 8.2 介面

- 置中覆蓋面板（modal overlay）
- 頂部搜尋框，即時過濾
- 結果按主機分組顯示
- 每個結果顯示：名稱、模式、狀態

### 8.3 操作

- 鍵盤上下鍵選取
- Enter: 開啟為新分頁（或切換到已開啟的分頁）
- Shift+Enter: 開啟在新工作區
- ESC: 關閉
- 目前定位為純 tmux session 選單，架構預留日後擴展為通用切換器

---

## 9. 檔案編輯器

### 9.1 功能

- 透過 daemon 讀寫遠端主機的檔案系統
- 程式碼檔案：語法高亮顯示
- Markdown 檔案：支援 Raw / Preview 模式切換
- 顯示檔案修改狀態

### 9.2 介面

- 頂部工具列：檔案路徑、修改狀態、模式切換按鈕、儲存按鈕
- 主體：程式碼/Markdown 內容
- 底部狀態：檔案類型、編碼、行列位置

### 9.3 開啟方式

- 側欄目錄面板點擊檔案
- Quick Switcher（未來擴展）
- 工作區內其他操作（如 git diff 點擊檔案）

---

## 10. 多主機管理

### 10.1 Host 結構

```typescript
interface Host {
  id: string
  name: string           // 顯示名稱（如 mlab, air-2019）
  address: string        // daemon 連線地址
  port: number           // daemon 端口
  status: 'connected' | 'disconnected' | 'connecting'
}
```

### 10.2 Host 管理

- 設定中新增/編輯/刪除 host
- 每個 host 獨立的 daemon 連線
- Session 清單按 host 分組
- 工作區可跨 host（混合不同主機的 sessions 和目錄）

### 10.3 連線管理

- 每個 host 獨立的 WebSocket 連線集合（terminal WS、session-events WS、stream WS）
- 自動重連機制（沿用現有的指數退避策略）
- 狀態列顯示當前分頁的 host 連線狀態

---

## 11. 狀態管理

### 11.1 新增 Store

現有三個 Zustand store 需要擴充，並新增：

| Store | 職責 |
|-------|------|
| `useTabStore` | 分頁狀態、排序、活躍分頁 |
| `useWorkspaceStore` | 工作區定義、目錄、分頁群組、Activity Bar 排序 |
| `useHostStore` | 多主機連線狀態 |
| `useSidebarStore` | 側欄 4 區域面板配置、模式（固定/預設/縮減）、寬度 |

> **注意**：不需要獨立的 BarStore。Activity Bar 的顯示內容由 WorkspaceStore + TabStore 推導（工作區列表 + 不屬於任何工作區的獨立分頁）。

### 11.2 持久化

以下狀態需持久化（localStorage 或 daemon 端）：

- 分頁列表和排序
- 工作區定義和釘選目錄
- Activity Bar 排序
- Host 清單
- 側欄 4 區域的面板配置、模式和寬度
- Quick Switcher 快捷鍵

### 11.3 現有 Store 影響

- `useSessionStore`: 保留，但 `activeId` 概念被 `useTabStore.activeTabId` 取代
- `useStreamStore`: 保留，每個 stream 分頁對應一個 session 的 stream 狀態
- `useConfigStore`: 擴充，增加 sidebar zone 配置等設定

---

## 12. 路由

### 12.1 從 Hash 路由升級

現有 `#/{uid}/{mode}` 需升級以支援分頁系統：

**方案：保留 hash 指向當前活躍分頁，完整狀態由 store 管理**

```
#/tab/{tabId}
```

分頁的完整資訊（類型、session、host 等）存在 store 中，hash 只負責指向活躍分頁。這樣可以支援直接 URL 分享和重新整理後恢復。

---

## 13. 後端 API 擴充

### 13.1 檔案系統 API（新增）

> **設計決策**：FS 和 Git API 統一使用 POST 方法（包含讀取操作），避免檔案路徑透過 URL query string 洩漏到 access log。

| 端點 | 方法 | 用途 |
|------|------|------|
| `/api/fs/read` | POST | 讀取檔案內容 |
| `/api/fs/write` | POST | 寫入檔案內容 |
| `/api/fs/list` | POST | 列出目錄內容 |
| `/api/fs/watch` | WS | 監看目錄/檔案變動 |
| `/api/fs/stat` | POST | 取得檔案/目錄資訊 |

### 13.2 Git API（新增）

| 端點 | 方法 | 用途 |
|------|------|------|
| `/api/git/log` | POST | 取得 git log |
| `/api/git/status` | POST | 取得 git status |
| `/api/git/branches` | POST | 列出 branches |

### 13.3 設定 API（擴充）

現有 `/api/config` 擴充支援：

- Host 清單
- 側欄 4 區域配置和面板模式
- 提示詞清單
- 工作區定義

---

## 14. 手機版（響應式設計）

### 14.1 佈局對應

| 桌面元素 | 手機對應 | 互動方式 |
|----------|----------|----------|
| Activity Bar | 左滑抽屜頂部的工作區 chips | 左滑開啟 / 點擊頂部工作區圖示 |
| 分頁列 | 頂部水平捲動分頁條 | 水平滑動瀏覽、點擊切換 |
| 側欄 4 區域 | 全螢幕抽屜（左滑叫出） | 所有面板合併在同一個抽屜中，用頂部切換 |
| 群組展開/收合 | 不適用 | 手機上用工作區 chips 切換 |
| Quick Switcher | 全螢幕搜尋 | 頂部按鈕觸發 |
| 縮減面板 auto-hide | 不適用 | 手機無 hover，統一用抽屜 |
| 狀態列 | 保留（底部窄條） | 精簡顯示 |

### 14.2 快速操作鍵盤

手機版增加**可自定義的快速操作鍵盤**（參考 Termius iOS SSH App）：

- 浮動在系統鍵盤上方的額外工具列
- 提供 Terminal 常用按鍵：Tab、Esc、Ctrl、Alt、方向鍵等
- 使用者可自定義按鍵排列和組合鍵
- 僅在 Terminal 分頁且鍵盤啟動時顯示

### 14.3 開發策略

手機版不獨立開發，而是在各 Phase 中一併處理：

- **Phase 1 起**：App shell 用 `@media` breakpoint 切換佈局，建立 `useIsMobile()` hook
- **各 Phase 中**：元件設計時將「面板內容」和「佈局容器」分離，手機版只是換容器
- **統一補齊**：Drawer 元件、TabBar mobile 變體、觸控手勢、快速操作鍵盤

---

## 15. 實作分期建議

### Phase 1：分頁系統 + Activity Bar 基礎
- TabStore + Tab 元件
- Activity Bar（工作區切換，暫時只有預設工作區 + 獨立分頁）
- 分頁列渲染（無群組）
- 多分頁切換 + 內容區域動態渲染
- 重構現有 App.tsx 佈局（Activity Bar + tab bar + content + status bar，側欄 4 區域留空殼）

### Phase 2：工作區
- WorkspaceStore + 工作區群組 UI
- Activity Bar 完整功能（多工作區切換、拖曳排序、右鍵選單）
- 橫列式展開/收合（子母層）
- 工作區色標 + 圖示自訂
- 分頁拖曳（群組內外）

### Phase 3：側欄面板系統
- SidebarStore + 4 區域面板框架
- 固定/預設/縮減三模式 + 智慧切換
- 縮減模式的 auto-hide 浮動展開
- Sessions 面板（重構現有 SessionPanel）
- 面板拖曳配置（後期優化）

### Phase 4：工作區面板
- 目錄面板 + 檔案變動監看（後端 FS watch API）
- Git 面板（後端 Git API）
- 工作區資訊面板
- AI 對話歷史面板

### Phase 5：檔案編輯器
- 後端 FS 讀寫 API
- Editor 分頁元件
- Markdown 預覽

### Phase 6：多主機
- HostStore + Host 管理 UI
- 多 daemon 連線架構
- Session 清單按 host 分組

### Phase 7：進階功能
- Quick Switcher
- 提示詞注入面板
- 側欄面板拖曳配置介面
- 手機版響應式

---

## 16. 技術考量

### 16.1 分頁效能

- Terminal 分頁：xterm.js 實例在非活躍時應保留（避免重連開銷），但可卸載 WebGL renderer
- Stream 分頁：訊息列表在非活躍時保留在 store，元件可卸載
- Editor 分頁：檔案內容快取在 store

### 16.2 拖曳實作

- 使用原生 HTML5 Drag and Drop API 或輕量拖曳庫
- 分頁拖曳目標：重新排序、移入工作區、移出工作區
- 視覺回饋：拖曳時顯示 drop indicator

### 16.3 檔案監看

- 後端使用 `fsnotify` 監看釘選目錄
- 透過 WebSocket 推送變動事件到前端
- 前端增量更新目錄樹狀態

### 16.4 多主機連線

- 每個 host 獨立的連線管理實例
- 共用現有的自動重連邏輯
- Store 中以 hostId 為 key 區分不同主機的資料
