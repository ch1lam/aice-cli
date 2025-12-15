import type {
  ProviderErrorChunk,
  ProviderStatusChunk,
  ProviderStream,
  ProviderTextChunk,
  ProviderUsageChunk,
  TokenUsage,
} from '../core/stream.js'

import { toError } from '../core/errors.js'

type AbortControllerLike = {
  abort: (reason?: unknown) => Promise<void> | void
}

export type AbortableAsyncIterable<T> = AsyncIterable<T> & {
  controller?: AbortControllerLike
}

export type ProviderStreamEvent =
  | { error: unknown; fallbackMessage: string; type: 'error' }
  | { text: string; type: 'text' }
  | { type: 'usage'; usage: TokenUsage }

export type ProviderStreamEventMapper<T> = (
  event: T,
) => null | ProviderStreamEvent | ProviderStreamEvent[] | undefined

export interface StreamProviderWithLifecycleOptions<T> {
  createStream: () => AbortableAsyncIterable<T> | Promise<AbortableAsyncIterable<T>>
  mapEvent: ProviderStreamEventMapper<T>
  startFallbackMessage: string
  streamFallbackMessage: string
}

function statusChunk(status: ProviderStatusChunk['status'], timestamp = Date.now()): ProviderStatusChunk {
  return { status, timestamp, type: 'status' }
}

function errorChunk(error: unknown, fallbackMessage: string, timestamp = Date.now()): ProviderErrorChunk {
  return { error: toError(error, fallbackMessage), timestamp, type: 'error' }
}

function textChunk(text: string, timestamp: number): ProviderTextChunk {
  return { text, timestamp, type: 'text' }
}

function usageChunk(usage: TokenUsage, timestamp = Date.now()): ProviderUsageChunk {
  return { timestamp, type: 'usage', usage }
}

function normalizeEvents<T>(events: null | T | T[] | undefined): T[] {
  if (!events) return []
  return Array.isArray(events) ? events : [events]
}

export async function* streamProviderWithLifecycle<T>(
  options: StreamProviderWithLifecycleOptions<T>,
): ProviderStream {
  yield statusChunk('running')

  let stream: AbortableAsyncIterable<T>

  try {
    stream = await options.createStream()
  } catch (error) {
    yield statusChunk('failed')
    yield errorChunk(error, options.startFallbackMessage)
    return
  }

  let latestUsage: TokenUsage | undefined

  try {
    for await (const event of stream) {
      const now = Date.now()
      const mapped = normalizeEvents(options.mapEvent(event))

      for (const item of mapped) {
        if (item.type === 'text') {
          yield textChunk(item.text, now)
          continue
        }

        if (item.type === 'usage') {
          latestUsage = item.usage
          continue
        }

        yield statusChunk('failed', now)
        yield errorChunk(item.error, item.fallbackMessage, now)
        return
      }
    }
  } catch (error) {
    yield statusChunk('failed')
    yield errorChunk(error, options.streamFallbackMessage)
    return
  } finally {
    await stream.controller?.abort()
  }

  if (latestUsage) {
    yield usageChunk(latestUsage)
  }

  yield statusChunk('completed')
}
