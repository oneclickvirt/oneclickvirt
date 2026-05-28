const ANSI_COLORS = {
  black: '#000000',
  red: '#cd3131',
  green: '#0dbc79',
  yellow: '#e5e510',
  blue: '#2472c8',
  magenta: '#bc3fbc',
  cyan: '#11a8cd',
  white: '#e5e5e5',
  brightBlack: '#666666',
  brightRed: '#f14c4c',
  brightGreen: '#23d18b',
  brightYellow: '#f5f543',
  brightBlue: '#3b8eea',
  brightMagenta: '#d670d6',
  brightCyan: '#29b8db',
  brightWhite: '#e5e5e5'
}

const getThemeValue = (styles, key, fallback) => {
  const value = styles.getPropertyValue(key).trim()
  return value || fallback
}

export const resolveTerminalTheme = () => {
  const styles = getComputedStyle(document.documentElement)
  const background = getThemeValue(styles, '--terminal-bg', '#0b1220')
  const foreground = getThemeValue(styles, '--terminal-foreground', '#d4d4d4')
  const cursor = getThemeValue(styles, '--terminal-cursor', foreground)

  return {
    background,
    foreground,
    cursor,
    ...ANSI_COLORS
  }
}

export const applyTerminalTheme = (terminal) => {
  if (!terminal) return
  terminal.options.theme = resolveTerminalTheme()
}
