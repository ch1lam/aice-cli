import type { ModelMessage } from 'ai'

import type { ProviderId, ProviderStream } from './stream.js'

export interface SessionRequest {
  messages: ModelMessage[]
  model: string
  providerId: ProviderId
  temperature?: number
  signal?: AbortSignal
}

export interface LLMProvider<Request extends SessionRequest = SessionRequest> {
  id: ProviderId
  stream(request: Request): ProviderStream
}
