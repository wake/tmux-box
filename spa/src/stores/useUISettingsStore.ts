import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type TerminalRenderer = 'webgl' | 'dom'

interface UISettings {
  /**
   * 收到第一筆 terminal data 後，延遲多久才移除 overlay 顯示畫面（ms）。
   *
   * 預設 300ms。這段延遲讓 overlay 遮住以下瞬態現象：
   * - 0ms（onOpen reveal）：tmux attach 完成但尚未送出畫面，會看到 resize 彈跳
   * - 立即 reveal（首筆 data 無 delay）：daemon batcher 16ms 批次 + Claude Code
   *   自動捲動會產生可見的捲動閃爍
   * - 300ms：足夠 batcher 送完初始畫面 + tmux 渲染穩定，視覺上平滑過渡
   *
   * fallback timeout = 此值 × 5（預設 1500ms），在完全收不到 data 時保底顯示。
   */
  terminalRevealDelay: number
  setTerminalRevealDelay: (ms: number) => void

  /**
   * Terminal 渲染器類型。
   *
   * - webgl：效能最佳，適合 Claude Code 大量高速輸出，但每個實例佔一個 WebGL context
   *   （瀏覽器限制通常 8-16 個）
   * - dom：DOM 渲染，效能較低但相容性最好，無 WebGL context 限制，適合 Electron 或低負載場景
   *
   * 變更後需重啟 terminal 連線（SettingsPanel 的「套用」會自動處理）。
   */
  terminalRenderer: TerminalRenderer
  setTerminalRenderer: (renderer: TerminalRenderer) => void
}

export const useUISettingsStore = create<UISettings>()(
  persist(
    (set) => ({
      terminalRevealDelay: 300,
      setTerminalRevealDelay: (ms) => set({ terminalRevealDelay: ms }),
      terminalRenderer: 'webgl' as TerminalRenderer,
      setTerminalRenderer: (renderer) => set({ terminalRenderer: renderer }),
    }),
    { name: 'tbox-ui-settings' },
  ),
)
