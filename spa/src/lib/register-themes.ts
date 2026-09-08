// spa/src/lib/register-themes.ts
import { registerTheme } from './theme-registry'
import type { ThemeTokens } from './theme-tokens'

const darkTokens: ThemeTokens = {
  'surface-primary': '#0a0a1a',
  'surface-secondary': '#12122a',
  'surface-tertiary': '#08081a',
  'surface-elevated': '#1e1e3e',
  'surface-hover': '#1a1a32',
  'surface-active': '#272444',
  'surface-input': '#2a2a2a',
  'text-primary': '#e0e0e0',
  'text-secondary': '#9ca3af',
  'text-muted': '#6b7280',
  'text-inverse': '#0a0a1a',
  'border-default': '#404040',
  'border-active': '#7a6aaa',
  'border-subtle': '#2a2a2a',
  'accent': '#7a6aaa',
  'accent-hover': '#8a7aba',
  'accent-muted': 'rgba(122, 106, 170, 0.3)',
  'terminal-bg': '#0a0a1a',
  'terminal-fg': '#e0e0e0',
  'terminal-cursor': '#e0e0e0',
  'status-error': '#e06c75',
  'status-warning': '#c8b560',
  'status-success': '#7ec699',
}

const lightTokens: ThemeTokens = {
  'surface-primary': '#f5f5f5',
  'surface-secondary': '#e8e8e8',
  'surface-tertiary': '#f0f0f0',
  'surface-elevated': '#ffffff',
  'surface-hover': '#e0e0e0',
  'surface-active': '#d4d0e8',
  'surface-input': '#ffffff',
  'text-primary': '#1a1a2e',
  'text-secondary': '#4a4a5a',
  'text-muted': '#8a8a9a',
  'text-inverse': '#f5f5f5',
  'border-default': '#d0d0d0',
  'border-active': '#6a5a9a',
  'border-subtle': '#e0e0e0',
  'accent': '#6a5a9a',
  'accent-hover': '#5a4a8a',
  'accent-muted': 'rgba(106, 90, 154, 0.15)',
  'terminal-bg': '#f5f5f5',
  'terminal-fg': '#1a1a2e',
  'terminal-cursor': '#1a1a2e',
  'status-error': '#fce4e4',
  'status-warning': '#fef3cd',
  'status-success': '#d4edda',
}

const nordTokens: ThemeTokens = {
  'surface-primary': '#2e3440',
  'surface-secondary': '#3b4252',
  'surface-tertiary': '#2e3440',
  'surface-elevated': '#434c5e',
  'surface-hover': '#434c5e',
  'surface-active': '#4c566a',
  'surface-input': '#3b4252',
  'text-primary': '#eceff4',
  'text-secondary': '#d8dee9',
  'text-muted': '#7b88a1',
  'text-inverse': '#2e3440',
  'border-default': '#4c566a',
  'border-active': '#88c0d0',
  'border-subtle': '#3b4252',
  'accent': '#88c0d0',
  'accent-hover': '#8fbcbb',
  'accent-muted': 'rgba(136, 192, 208, 0.2)',
  'terminal-bg': '#2e3440',
  'terminal-fg': '#d8dee9',
  'terminal-cursor': '#d8dee9',
  'status-error': '#bf616a33',
  'status-warning': '#ebcb8b',
  'status-success': '#a3be8c33',
}

const draculaTokens: ThemeTokens = {
  'surface-primary': '#282a36',
  'surface-secondary': '#21222c',
  'surface-tertiary': '#191a21',
  'surface-elevated': '#44475a',
  'surface-hover': '#44475a',
  'surface-active': '#6272a4',
  'surface-input': '#21222c',
  'text-primary': '#f8f8f2',
  'text-secondary': '#bfbfbf',
  'text-muted': '#6272a4',
  'text-inverse': '#282a36',
  'border-default': '#44475a',
  'border-active': '#bd93f9',
  'border-subtle': '#21222c',
  'accent': '#bd93f9',
  'accent-hover': '#caa9fa',
  'accent-muted': 'rgba(189, 147, 249, 0.2)',
  'terminal-bg': '#282a36',
  'terminal-fg': '#f8f8f2',
  'terminal-cursor': '#f8f8f2',
  'status-error': '#ff555533',
  'status-warning': '#f1fa8c',
  'status-success': '#50fa7b33',
}

export function registerBuiltinThemes(): void {
  registerTheme({ id: 'dark', name: 'Dark', tokens: darkTokens, builtin: true })
  registerTheme({ id: 'light', name: 'Light', tokens: lightTokens, builtin: true })
  registerTheme({ id: 'nord', name: 'Nord', tokens: nordTokens, builtin: true })
  registerTheme({ id: 'dracula', name: 'Dracula', tokens: draculaTokens, builtin: true })
}
