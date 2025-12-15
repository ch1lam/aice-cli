export type MessageRole = 'assistant' | 'system' | 'user'

export interface PromptMessage {
  role: MessageRole
  text: string
}

export interface ChatMessage extends PromptMessage {
  id: number
}
