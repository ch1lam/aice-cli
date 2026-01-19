import type { ModelMessage } from 'ai'

import type { PromptMessage } from '../types/chat.js'

export interface BuildMessagesOptions {
  maxMessages?: number
}

export function buildMessages(
  history: PromptMessage[],
  options: BuildMessagesOptions = {},
): ModelMessage[] {
  const truncatedHistory = truncateHistory(history, options.maxMessages)

  return truncatedHistory.map(message => ({
    content: message.text,
    role: message.role,
  }))
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
