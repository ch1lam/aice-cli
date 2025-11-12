import type {
  ProviderId,
  ProviderStream,
  SessionMetaChunk,
  SessionStream,
  SessionTextChunk,
} from './stream.js'

export interface SessionRequest {
  model: string
  prompt: string
  providerId: ProviderId
  signal?: AbortSignal
  systemPrompt?: string
  user?: string
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

  let tokenIndex = 0
  let encounteredError = false

  const metaChunk: SessionMetaChunk = {
    model: request.model,
    providerId: provider.id,
    timestamp: Date.now(),
    type: 'meta',
  }

  yield metaChunk

  for await (const chunk of provider.stream(request)) {
    if (chunk.type === 'text') {
      const indexedChunk: SessionTextChunk = {
        ...chunk,
        index: tokenIndex++,
      }

      yield indexedChunk
      continue
    }

    yield chunk

    if (chunk.type === 'error') {
      encounteredError = true
      break
    }
  }

  if (!encounteredError) {
    yield {
      timestamp: Date.now(),
      type: 'done',
    }
  }
}
