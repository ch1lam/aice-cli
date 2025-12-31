import type {
  ProviderId,
  ProviderStream,
  SessionMetaChunk,
  SessionStream,
} from '../types/stream.js'

export interface SessionRequest {
  model: string
  prompt: string
  providerId: ProviderId
  signal?: AbortSignal
  systemPrompt?: string
}

export interface LLMProvider<Request extends SessionRequest = SessionRequest> {
  id: ProviderId
  stream(request: Request): ProviderStream
}

export async function* runSession(
  provider: LLMProvider,
  request: SessionRequest,
): SessionStream {
  if (provider.id !== request.providerId) {
    throw new Error(`Provider mismatch: expected ${request.providerId}, got ${provider.id}`)
  }

  const metaChunk: SessionMetaChunk = {
    model: request.model,
    providerId: provider.id,
    type: 'meta',
  }

  yield metaChunk

  for await (const chunk of provider.stream(request)) {
    yield chunk
  }
}
