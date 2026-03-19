// spa/src/components/SettingsPanel.tsx
import { useState, useEffect } from 'react'
import { X, Plus, Trash } from '@phosphor-icons/react'
import { useConfigStore } from '../stores/useConfigStore'

interface Props {
  daemonBase: string
  onClose: () => void
}

interface PresetRow {
  name: string
  command: string
}

export default function SettingsPanel({ daemonBase, onClose }: Props) {
  const { config, fetch: fetchConfig, update } = useConfigStore()

  const [autoResize, setAutoResize] = useState(true)
  const [ignoreSize, setIgnoreSize] = useState(false)
  const [streamPresets, setStreamPresets] = useState<PresetRow[]>([])
  const [jsonlPresets, setJsonlPresets] = useState<PresetRow[]>([])
  const [ccCommands, setCcCommands] = useState<string[]>([])
  const [pollInterval, setPollInterval] = useState(5)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // Seed local state from config
  useEffect(() => {
    if (config) {
      setAutoResize(config.terminal?.auto_resize !== false)
      setIgnoreSize(config.terminal?.ignore_size === true)
      setStreamPresets(config.stream?.presets?.map(p => ({ ...p })) || [])
      setJsonlPresets(config.jsonl?.presets?.map(p => ({ ...p })) || [])
      setCcCommands([...(config.detect?.cc_commands || [])])
      setPollInterval(config.detect?.poll_interval || 5)
    }
  }, [config])

  // Refresh config on mount if missing
  useEffect(() => {
    if (!config) fetchConfig(daemonBase)
  }, [config, fetchConfig, daemonBase])

  function updateStreamPreset(idx: number, field: 'name' | 'command', value: string) {
    setStreamPresets(prev => prev.map((p, i) => i === idx ? { ...p, [field]: value } : p))
  }

  function removeStreamPreset(idx: number) {
    setStreamPresets(prev => prev.filter((_, i) => i !== idx))
  }

  function addStreamPreset() {
    setStreamPresets(prev => [...prev, { name: '', command: '' }])
  }

  function updateJsonlPreset(idx: number, field: 'name' | 'command', value: string) {
    setJsonlPresets(prev => prev.map((p, i) => i === idx ? { ...p, [field]: value } : p))
  }

  function removeJsonlPreset(idx: number) {
    setJsonlPresets(prev => prev.filter((_, i) => i !== idx))
  }

  function addJsonlPreset() {
    setJsonlPresets(prev => [...prev, { name: '', command: '' }])
  }

  function updateCcCommand(idx: number, value: string) {
    setCcCommands(prev => prev.map((c, i) => i === idx ? value : c))
  }

  function removeCcCommand(idx: number) {
    setCcCommands(prev => prev.filter((_, i) => i !== idx))
  }

  function addCcCommand() {
    setCcCommands(prev => [...prev, ''])
  }

  async function handleSave() {
    setSaving(true)
    setError(null)
    try {
      await update(daemonBase, {
        terminal: {
          auto_resize: autoResize,
          ignore_size: ignoreSize,
        },
        stream: { presets: streamPresets.filter(p => p.name.trim()) },
        jsonl: { presets: jsonlPresets.filter(p => p.name.trim()) },
        detect: {
          cc_commands: ccCommands.filter(c => c.trim()),
          poll_interval: pollInterval,
        },
      })
      onClose()
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex justify-end" data-testid="settings-panel">
      {/* Backdrop */}
      <div className="absolute inset-0 bg-black/50" onClick={onClose} />

      {/* Panel */}
      <div className="relative w-[420px] max-w-full bg-[#1e1e1e] border-l border-[#404040] h-full overflow-y-auto">
        {/* Header */}
        <div className="flex items-center justify-between px-4 py-3 border-b border-[#404040]">
          <h2 className="text-sm font-medium text-[#e5e5e5]">Settings</h2>
          <button onClick={onClose} className="text-[#888] hover:text-[#ccc] cursor-pointer" data-testid="settings-close">
            <X size={18} />
          </button>
        </div>

        <div className="p-4 space-y-6">
          {/* Terminal */}
          <section>
            <h3 className="text-xs uppercase text-[#999] mb-2">Terminal</h3>
            <span className="block text-[11px] text-[#777] mb-3">變更後需重新連線生效</span>
            <div className="space-y-3">
              <label className="flex items-center justify-between cursor-pointer">
                <div>
                  <span className="text-xs text-[#ddd]">Auto Resize</span>
                  <span className="block text-[11px] text-[#777]">連線後自動調整 tmux 視窗尺寸</span>
                </div>
                <input
                  data-testid="terminal-auto-resize"
                  type="checkbox"
                  checked={autoResize}
                  onChange={e => setAutoResize(e.target.checked)}
                  className="w-4 h-4 accent-blue-500 cursor-pointer"
                />
              </label>
              <label className="flex items-center justify-between cursor-pointer">
                <div>
                  <span className="text-xs text-[#ddd]">Ignore Size</span>
                  <span className="block text-[11px] text-[#777]">relay 不影響 tmux 視窗尺寸（保護 iTerm）</span>
                </div>
                <input
                  data-testid="terminal-ignore-size"
                  type="checkbox"
                  checked={ignoreSize}
                  onChange={e => setIgnoreSize(e.target.checked)}
                  className="w-4 h-4 accent-blue-500 cursor-pointer"
                />
              </label>
            </div>
          </section>

          {/* Stream Presets */}
          <section>
            <h3 className="text-xs uppercase text-[#999] mb-2">Stream Presets</h3>
            <div className="space-y-2">
              {streamPresets.map((p, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    data-testid={`stream-preset-name-${i}`}
                    value={p.name}
                    onChange={e => updateStreamPreset(i, 'name', e.target.value)}
                    placeholder="name"
                    className="flex-1 bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd] placeholder:text-[#555]"
                  />
                  <input
                    data-testid={`stream-preset-cmd-${i}`}
                    value={p.command}
                    onChange={e => updateStreamPreset(i, 'command', e.target.value)}
                    placeholder="command"
                    className="flex-[2] bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd] placeholder:text-[#555]"
                  />
                  <button onClick={() => removeStreamPreset(i)} className="text-red-400 hover:text-red-300 cursor-pointer" data-testid={`stream-preset-del-${i}`}>
                    <Trash size={14} />
                  </button>
                </div>
              ))}
            </div>
            <button onClick={addStreamPreset} data-testid="stream-preset-add"
              className="mt-2 flex items-center gap-1 text-xs text-[#888] hover:text-[#ccc] cursor-pointer">
              <Plus size={12} /> Add preset
            </button>
          </section>

          {/* JSONL Presets */}
          <section>
            <h3 className="text-xs uppercase text-[#999] mb-2">
              JSONL Presets
              <span className="ml-2 text-[10px] text-yellow-500 normal-case">(Phase 3)</span>
            </h3>
            <div className="space-y-2">
              {jsonlPresets.map((p, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    data-testid={`jsonl-preset-name-${i}`}
                    value={p.name}
                    onChange={e => updateJsonlPreset(i, 'name', e.target.value)}
                    placeholder="name"
                    className="flex-1 bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd] placeholder:text-[#555]"
                  />
                  <input
                    data-testid={`jsonl-preset-cmd-${i}`}
                    value={p.command}
                    onChange={e => updateJsonlPreset(i, 'command', e.target.value)}
                    placeholder="command"
                    className="flex-[2] bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd] placeholder:text-[#555]"
                  />
                  <button onClick={() => removeJsonlPreset(i)} className="text-red-400 hover:text-red-300 cursor-pointer" data-testid={`jsonl-preset-del-${i}`}>
                    <Trash size={14} />
                  </button>
                </div>
              ))}
            </div>
            <button onClick={addJsonlPreset} data-testid="jsonl-preset-add"
              className="mt-2 flex items-center gap-1 text-xs text-[#888] hover:text-[#ccc] cursor-pointer">
              <Plus size={12} /> Add preset
            </button>
          </section>

          {/* CC Detect Commands */}
          <section>
            <h3 className="text-xs uppercase text-[#999] mb-2">CC Detect Commands</h3>
            <div className="space-y-2">
              {ccCommands.map((cmd, i) => (
                <div key={i} className="flex items-center gap-2">
                  <input
                    data-testid={`cc-cmd-${i}`}
                    value={cmd}
                    onChange={e => updateCcCommand(i, e.target.value)}
                    placeholder="e.g. claude"
                    className="flex-1 bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd] placeholder:text-[#555]"
                  />
                  <button onClick={() => removeCcCommand(i)} className="text-red-400 hover:text-red-300 cursor-pointer" data-testid={`cc-cmd-del-${i}`}>
                    <Trash size={14} />
                  </button>
                </div>
              ))}
            </div>
            <button onClick={addCcCommand} data-testid="cc-cmd-add"
              className="mt-2 flex items-center gap-1 text-xs text-[#888] hover:text-[#ccc] cursor-pointer">
              <Plus size={12} /> Add command
            </button>
          </section>

          {/* Poll Interval */}
          <section>
            <h3 className="text-xs uppercase text-[#999] mb-2">Poll Interval (seconds)</h3>
            <input
              data-testid="poll-interval"
              type="number"
              min={1}
              max={60}
              value={pollInterval}
              onChange={e => setPollInterval(Number(e.target.value) || 5)}
              className="w-20 bg-[#2a2a2a] border border-[#404040] rounded px-2 py-1 text-xs text-[#ddd]"
            />
          </section>

          {/* Save */}
          <button
            data-testid="settings-save"
            onClick={handleSave}
            disabled={saving}
            className="w-full bg-blue-600 hover:bg-blue-500 disabled:opacity-50 text-white text-sm py-2 rounded cursor-pointer transition-colors"
          >
            {saving ? 'Saving...' : 'Save'}
          </button>
          {error && <p className="text-red-400 text-xs mt-2" data-testid="settings-error">{error}</p>}
        </div>
      </div>
    </div>
  )
}
