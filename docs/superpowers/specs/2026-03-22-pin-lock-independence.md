# Pin/Lock 獨立化 + Lock Icon 色彩修正

**日期**：2026-03-22
**範圍**：規格變更 — pin 與 lock 解耦為獨立旗標

## 背景

目前 `pinTab` 自動設定 `locked=true`，pinned tab 無法被關閉。使用者希望 pin 和 lock 各自獨立：pin 只負責定位（icon-only 釘左側），lock 只負責擋關閉。

## 需求

### Pin/Lock 獨立化

1. **`pinTab`**：只設 `pinned=true`，不動 `locked`
2. **`unpinTab`**：只設 `pinned=false`，不動 `locked`
3. **pinned tab 可關閉**：除非同時有 `locked=true`
4. **`unlockTab` 對 pinned tab 生效**：不再是 no-op
5. **reopen 恢復 pinned**：關閉的 pinned tab 被重新開啟時自動恢復 `pinned=true`

### dismissedSessions 格式變更

現行 `string[]` 改為物件陣列，保留 pinned 狀態供 reopen 使用：

```ts
// 舊格式
dismissedSessions: string[]

// 新格式
dismissedSessions: { sessionName: string; pinned: boolean }[]
```

### Persist Migration v2→v3

```ts
if (version < 3) {
  persisted.dismissedSessions = (persisted.dismissedSessions ?? [])
    .map((s: string) => ({ sessionName: s, pinned: false }))
}
```

### TabContextMenu 調整

- 「解鎖分頁」：移除 `!tab.pinned` 條件，改為 `tab.locked`（pinned 也可以解鎖）
- 「關閉分頁」：disabled 條件維持 `tab.locked`（不變）

### Lock Icon 色彩修正

目前 Lock icon 固定 `text-gray-600`，與 tab 文字色不一致。

改為繼承 tab 文字色（移除固定色 class）：

| Tab 狀態 | Tab 文字色 | Lock icon（繼承） |
|----------|-----------|-----------------|
| inactive | `text-gray-500` | `text-gray-500` |
| hover | `text-gray-300`（group-hover） | `text-gray-300` |
| active | `text-white` | `text-white` |

實作：SortableTab.tsx 的 Lock 元件移除 `text-gray-600` class，文字色自動繼承父層 tab 的 className。

## 影響檔案

| 檔案 | 變更 |
|------|------|
| `useTabStore.ts` | `pinTab` 不設 locked；`dismissTab` 存 `{ sessionName, pinned }`；migration v2→v3；`unlockTab` 移除 pinned guard |
| `useTabStore.pin-lock.test.ts` | 更新測試期望值 |
| `App.tsx` | `reopenClosed` / `handleSessionSelect` 讀取 dismissed pinned 狀態恢復 |
| `TabContextMenu.tsx` | 「解鎖分頁」show 條件移除 `!tab.pinned` |
| `SortableTab.tsx` | Lock icon 移除 `text-gray-600` |
| `types/tab.ts` | 無（Tab interface 不變） |

## 不變的部分

- locked tab 仍擋關閉（removeTab/dismissTab guard）
- Tab UI 外觀不變（pinned 仍 icon-only 釘左側）
- 拖曳分區限制不變
- `updateTab` 仍 strip pinned/locked

## 測試重點

- pinTab 不改 locked（原本 false 維持 false）
- unpinTab 不改 locked（原本 true 維持 true）
- pinned + unlocked tab 可關閉
- pinned + locked tab 不可關閉
- dismissTab 存 pinned 狀態
- reopen pinned tab 恢復 pinned=true 且移入 pinned zone
- reopen 普通 tab 恢復 pinned=false
- migration v2→v3 正確轉換
- unlockTab 對 pinned tab 生效
- Lock icon 繼承 tab 文字色
