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

export function cycleProviderChoice(current: number, delta: number, total: number): number {
  return cycleIndex(current, delta, total)
}
