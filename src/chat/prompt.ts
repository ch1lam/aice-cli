import type { PromptMessage } from '../domain/chat/index.js'

export interface BuildPromptOptions {
  maxMessages?: number
}

export function buildPrompt(history: PromptMessage[], options: BuildPromptOptions = {}): string {
  const filteredHistory = history.filter(message => message.role !== 'system')
  const truncatedHistory = truncateHistory(filteredHistory, options.maxMessages)

  const lines = truncatedHistory.map(message => formatPromptLine(message))
  lines.push('Assistant:')

  return lines.join('\n')
}

function formatPromptLine(message: PromptMessage): string {
  const label = message.role === 'assistant' ? 'Assistant' : 'User'
  return `${label}: ${message.text}`
}

function truncateHistory(history: PromptMessage[], maxMessages?: number): PromptMessage[] {
  if (typeof maxMessages !== 'number' || Number.isNaN(maxMessages) || !Number.isFinite(maxMessages)) {
    return history
  }

  const limit = Math.max(0, Math.floor(maxMessages))
  if (limit === 0) return []
  if (history.length <= limit) return history

  return history.slice(-limit)
}
