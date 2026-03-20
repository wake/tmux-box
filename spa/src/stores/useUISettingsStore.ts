import { create } from 'zustand'
import { persist } from 'zustand/middleware'

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
}

export const useUISettingsStore = create<UISettings>()(
  persist(
    (set) => ({
      terminalRevealDelay: 300,
      setTerminalRevealDelay: (ms) => set({ terminalRevealDelay: ms }),
    }),
    { name: 'tbox-ui-settings' },
  ),
)
