import { useEffect, useRef, useState } from 'react'

import type {
  ProviderId,
  SessionStream,
  SessionStreamChunk,
  StreamStatus,
  TokenUsage,
} from '../../core/stream.js'

export interface SessionMeta {
  model: string
  providerId: ProviderId
}

export interface SessionState {
  content: string
  done: boolean
  error?: Error
  meta?: SessionMeta
  status?: StreamStatus
  statusMessage?: string
  usage?: TokenUsage
}

export interface UseSessionOptions {
  onError?: (error: Error) => void
  onStatusChange?: (status?: StreamStatus) => void
  stream?: SessionStream
}

export function useSession(options: UseSessionOptions): SessionState {
  const { onError, onStatusChange, stream } = options
  const [state, setState] = useState<SessionState>(() => createInitialState(Boolean(stream)))
  const lastStreamRef = useRef<SessionStream | undefined>(stream)
  const streamChanged = stream !== lastStreamRef.current

  if (streamChanged) {
    lastStreamRef.current = stream
  }

  useEffect(() => {
    setState(createInitialState(Boolean(stream)))
  }, [stream])

  useEffect(() => {
    if (!stream) return

    let cancelled = false
    let content = ''
    let sawDone = false
    let sawError = false

    async function readStream(stream: SessionStream) {
      try {
        for await (const chunk of stream) {
          if (cancelled) break
          consumeChunk(chunk)
          if (chunk.type === 'done' || chunk.type === 'error') break
        }

        if (cancelled || sawDone || sawError) return

        setState(current => {
          if (current.done || current.status === 'failed') return current
          return {
            ...current,
            done: true,
            status: 'completed',
            statusMessage: undefined,
          }
        })
        onStatusChange?.('completed')
      } catch (error_: unknown) {
        const error = error_ instanceof Error ? error_ : new Error(String(error_))
        if (!cancelled) {
          setState(current => ({
            ...current,
            done: true,
            error,
            status: 'failed',
            statusMessage: error.message,
          }))
          onError?.(error)
          onStatusChange?.('failed')
        }
      }
    }

    function consumeChunk(chunk: SessionStreamChunk) {
      switch (chunk.type) {
        case 'done': {
          sawDone = true
          setState(current => ({
            ...current,
            done: true,
            status: 'completed',
            statusMessage: undefined,
          }))
          onStatusChange?.('completed')
          break
        }

        case 'error': {
          sawError = true
          setState(current => ({
            ...current,
            done: true,
            error: chunk.error,
            status: 'failed',
            statusMessage: chunk.error.message,
          }))
          onError?.(chunk.error)
          onStatusChange?.('failed')
          break
        }

        case 'meta': {
          setState(current => ({
            ...current,
            meta: { model: chunk.model, providerId: chunk.providerId },
          }))
          break
        }

        case 'status': {
          setState(current => ({
            ...current,
            status: chunk.status,
            statusMessage: chunk.detail,
          }))
          onStatusChange?.(chunk.status)
          break
        }

        case 'text': {
          content += chunk.text
          setState(current => ({
            ...current,
            content,
          }))
          break
        }

        case 'usage': {
          setState(current => ({
            ...current,
            usage: chunk.usage,
          }))
          break
        }
      }
    }

    readStream(stream)

    return () => {
      cancelled = true
    }
  }, [onError, onStatusChange, stream])

  return streamChanged ? createInitialState(Boolean(stream)) : state
}

function createInitialState(active: boolean): SessionState {
  return {
    content: '',
    done: false,
    error: undefined,
    meta: undefined,
    status: active ? 'running' : undefined,
    statusMessage: undefined,
    usage: undefined,
  }
}
