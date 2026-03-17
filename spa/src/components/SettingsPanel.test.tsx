// spa/src/components/SettingsPanel.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react'
import SettingsPanel from './SettingsPanel'
import { useConfigStore } from '../stores/useConfigStore'

vi.mock('../lib/api', () => ({
  getConfig: vi.fn().mockResolvedValue({
    bind: '0.0.0.0',
    port: 7860,
    stream: { presets: [{ name: 'cc', command: 'claude --dangerously-skip-permissions' }] },
    jsonl: { presets: [] },
    detect: { cc_commands: ['claude'], poll_interval: 5 },
  }),
  updateConfig: vi.fn().mockImplementation((_base: string, updates: Record<string, unknown>) =>
    Promise.resolve({ bind: '0.0.0.0', port: 7860, ...updates }),
  ),
  listSessions: vi.fn().mockResolvedValue([]),
}))

beforeEach(() => {
  cleanup()
  useConfigStore.setState({
    config: {
      bind: '0.0.0.0',
      port: 7860,
      stream: { presets: [{ name: 'cc', command: 'claude --dangerously-skip-permissions' }] },
      jsonl: { presets: [] },
      detect: { cc_commands: ['claude'], poll_interval: 5 },
    },
    loading: false,
  })
})

describe('SettingsPanel', () => {
  it('renders settings panel', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    expect(screen.getByTestId('settings-panel')).toBeInTheDocument()
    expect(screen.getByText('Settings')).toBeInTheDocument()
  })

  it('shows stream presets from config', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    const nameInput = screen.getByTestId('stream-preset-name-0') as HTMLInputElement
    expect(nameInput.value).toBe('cc')
  })

  it('adds a new stream preset', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    fireEvent.click(screen.getByTestId('stream-preset-add'))
    expect(screen.getByTestId('stream-preset-name-1')).toBeInTheDocument()
  })

  it('removes a stream preset', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    fireEvent.click(screen.getByTestId('stream-preset-del-0'))
    expect(screen.queryByTestId('stream-preset-name-0')).toBeNull()
  })

  it('shows cc detect commands', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    const cmdInput = screen.getByTestId('cc-cmd-0') as HTMLInputElement
    expect(cmdInput.value).toBe('claude')
  })

  it('updates poll interval', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    const input = screen.getByTestId('poll-interval') as HTMLInputElement
    fireEvent.change(input, { target: { value: '10' } })
    expect(input.value).toBe('10')
  })

  it('calls onClose when clicking close button', () => {
    const onClose = vi.fn()
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={onClose} />)
    fireEvent.click(screen.getByTestId('settings-close'))
    expect(onClose).toHaveBeenCalled()
  })

  it('calls onClose when clicking backdrop', () => {
    const onClose = vi.fn()
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={onClose} />)
    // The backdrop is the first child of the settings-panel with bg-black/50
    const backdrop = screen.getByTestId('settings-panel').querySelector('.bg-black\\/50')
    if (backdrop) fireEvent.click(backdrop)
    expect(onClose).toHaveBeenCalled()
  })

  it('saves config on save button click', async () => {
    const onClose = vi.fn()
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={onClose} />)
    fireEvent.click(screen.getByTestId('settings-save'))
    await waitFor(() => {
      expect(onClose).toHaveBeenCalled()
    })
  })

  it('shows Phase 3 note on JSONL section', () => {
    render(<SettingsPanel daemonBase="http://localhost:7860" onClose={vi.fn()} />)
    expect(screen.getByText('(Phase 3)')).toBeInTheDocument()
  })
})
