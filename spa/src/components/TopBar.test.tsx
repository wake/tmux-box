// spa/src/components/TopBar.test.tsx
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, cleanup } from '@testing-library/react'
import TopBar from './TopBar'

const defaultProps = {
  sessionName: 'test',
  mode: 'term',
  streamPresets: [{ name: 'cc', command: 'claude --dangerously-skip-permissions' }],
  jsonlPresets: [{ name: 'cc-jsonl', command: 'claude --output-format stream-json' }],
  onModeChange: vi.fn(),
  onHandoff: vi.fn(),
  onInterrupt: vi.fn(),
}

beforeEach(() => {
  cleanup()
  defaultProps.onModeChange = vi.fn()
  defaultProps.onHandoff = vi.fn()
  defaultProps.onInterrupt = vi.fn()
})

describe('TopBar', () => {
  it('shows session name', () => {
    render(<TopBar {...defaultProps} sessionName="my-project" />)
    expect(screen.getByText('my-project')).toBeInTheDocument()
  })

  it('shows all three mode buttons', () => {
    render(<TopBar {...defaultProps} />)
    expect(screen.getByTestId('mode-btn-term')).toBeInTheDocument()
    expect(screen.getByTestId('mode-btn-jsonl')).toBeInTheDocument()
    expect(screen.getByTestId('mode-btn-stream')).toBeInTheDocument()
  })

  it('highlights active mode', () => {
    render(<TopBar {...defaultProps} mode="stream" />)
    expect(screen.getByTestId('mode-btn-stream').className).toContain('bg-[#404040]')
    expect(screen.getByTestId('mode-btn-term').className).toContain('text-[#888]')
  })

  it('calls onModeChange when clicking term', () => {
    render(<TopBar {...defaultProps} />)
    fireEvent.click(screen.getByTestId('mode-btn-term'))
    expect(defaultProps.onModeChange).toHaveBeenCalledWith('term')
  })

  it('calls onHandoff directly with single preset', () => {
    render(<TopBar {...defaultProps} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(defaultProps.onHandoff).toHaveBeenCalledWith('stream', 'cc')
  })

  it('opens dropdown with multiple presets', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(screen.getByTestId('dropdown-stream')).toBeInTheDocument()
    expect(screen.getByText('cc')).toBeInTheDocument()
    expect(screen.getByText('gemini')).toBeInTheDocument()
  })

  it('calls onHandoff when selecting from dropdown', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    fireEvent.click(screen.getByText('gemini'))
    expect(defaultProps.onHandoff).toHaveBeenCalledWith('stream', 'gemini')
  })

  it('closes dropdown on second click', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(screen.getByTestId('dropdown-stream')).toBeInTheDocument()
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(screen.queryByTestId('dropdown-stream')).toBeNull()
  })

  it('shows interrupt button in stream mode', () => {
    render(<TopBar {...defaultProps} mode="stream" />)
    expect(screen.getByTestId('interrupt-btn')).toBeInTheDocument()
  })

  it('shows interrupt button in jsonl mode', () => {
    render(<TopBar {...defaultProps} mode="jsonl" />)
    expect(screen.getByTestId('interrupt-btn')).toBeInTheDocument()
  })

  it('hides interrupt button in term mode', () => {
    render(<TopBar {...defaultProps} mode="term" />)
    expect(screen.queryByTestId('interrupt-btn')).toBeNull()
  })

  it('shows dropdown caret only with multiple presets', () => {
    render(<TopBar {...defaultProps} streamPresets={[{ name: 'cc', command: 'claude' }]} />)
    const btn = screen.getByTestId('mode-btn-stream')
    expect(btn.textContent).not.toContain('▾')
  })

  it('shows dropdown caret with multiple presets', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    const btn = screen.getByTestId('mode-btn-stream')
    expect(btn.textContent).toContain('▾')
  })

  it('renders buttons in order: term → stream → jsonl', () => {
    render(<TopBar {...defaultProps} />)
    const modeSwitch = screen.getByTestId('mode-switch')
    const buttons = modeSwitch.querySelectorAll('button')
    // First button = term, second = stream, third = jsonl
    expect(buttons[0].textContent).toContain('term')
    expect(buttons[1].textContent).toContain('stream')
    expect(buttons[2].textContent).toContain('jsonl')
  })

  it('closes dropdown on Escape key', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(screen.getByTestId('dropdown-stream')).toBeInTheDocument()
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(screen.queryByTestId('dropdown-stream')).toBeNull()
  })

  it('closes dropdown on click outside', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    expect(screen.getByTestId('dropdown-stream')).toBeInTheDocument()
    fireEvent.mouseDown(document.body)
    expect(screen.queryByTestId('dropdown-stream')).toBeNull()
  })

  it('highlights active preset in dropdown', () => {
    const multiPresets = [
      { name: 'cc', command: 'claude' },
      { name: 'gemini', command: 'gemini-cli' },
    ]
    render(<TopBar {...defaultProps} streamPresets={multiPresets} activePreset="cc" />)
    fireEvent.click(screen.getByTestId('mode-btn-stream'))
    const ccButton = screen.getByText('cc')
    expect(ccButton.className).toContain('bg-[#404040]')
    expect(ccButton.className).toContain('text-white')
  })
})
