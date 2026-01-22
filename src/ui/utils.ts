import stringWidth from 'string-width'

export function clampIndex(index: number, length: number): number {
  if (length <= 0) return 0
  if (index < 0) return 0
  if (index >= length) return length - 1
  return index
}

export function cycleIndex(current: number, delta: number, length: number): number {
  if (length <= 0) return 0
  return (current + delta + length) % length
}

export function truncateByWidth(text: string, maxWidth: number): string {
  if (maxWidth <= 0 || !text) return ''

  let width = 0
  let result = ''
  for (const char of text) {
    const charWidth = stringWidth(char)
    if (width + charWidth > maxWidth) break
    width += charWidth
    result += char
  }

  return result
}

export function wrapByWidth(text: string, maxWidth: number): string[] {
  if (maxWidth <= 0) return ['']
  if (text === '') return ['']

  const normalized = text.replaceAll('\r\n', '\n')
  const rawLines = normalized.split('\n')
  const wrapped: string[] = []

  for (const rawLine of rawLines) {
    if (rawLine === '') {
      wrapped.push('')
      continue
    }

    let line = ''
    let width = 0
    for (const char of rawLine) {
      const charWidth = stringWidth(char)
      if (width + charWidth > maxWidth) {
        wrapped.push(line)
        line = char
        width = charWidth
      } else {
        line += char
        width += charWidth
      }
    }

    wrapped.push(line)
  }

  return wrapped.length > 0 ? wrapped : ['']
}
