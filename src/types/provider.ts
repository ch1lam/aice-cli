import type { ModelMessage } from 'ai'

export interface ProviderRequestInput {
  messages: ModelMessage[]
  model?: string
  temperature?: number
}
