import { useEffect, useRef, useState } from 'react'

import type { SessionMeta } from '../../types/session-meta.js'
import type { SessionStream, SessionStreamChunk, StreamStatus, TokenUsage } from '../../types/stream.js'

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
          if (chunk.type === 'finish' || chunk.type === 'error' || chunk.type === 'abort') break
        }

        if (cancelled || sawDone || sawError) return

        setState(current => {
          if (current.done || current.status === 'failed') return current
          return {
            ...current,
            content,
            done: true,
            status: 'completed',
            statusMessage: undefined,
          }
        })
        onStatusChange?.('completed')
      } catch (error_: unknown) {
        const error = normalizeError(error_)
        if (!cancelled) {
          setState(current => ({
            ...current,
            content,
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

    function appendText(text: string) {
      content += text
      setState(current => ({
        ...current,
        content,
      }))
    }

    function consumeChunk(chunk: SessionStreamChunk) {
      switch (chunk.type) {
        case 'abort': {
          sawDone = true
          setState(current => ({
            ...current,
            content,
            done: true,
            status: 'aborted',
            statusMessage: 'Aborted',
          }))
          onStatusChange?.('aborted')
          break
        }

        case 'error': {
          sawError = true
          const error = normalizeError(chunk.error)
          setState(current => ({
            ...current,
            content,
            done: true,
            error,
            status: 'failed',
            statusMessage: error.message,
          }))
          onError?.(error)
          onStatusChange?.('failed')
          break
        }

        case 'finish': {
          sawDone = true
          setState(current => ({
            ...current,
            content,
            done: true,
            status: 'completed',
            statusMessage: chunk.finishReason === 'stop' ? undefined : chunk.finishReason,
            usage: chunk.totalUsage,
          }))
          onStatusChange?.('completed')
          break
        }

        case 'finish-step': {
          setState(current => ({
            ...current,
            usage: chunk.usage,
          }))
          break
        }

        case 'meta': {
          setState(current => ({
            ...current,
            meta: { model: chunk.model, providerId: chunk.providerId },
          }))
          break
        }

        case 'reasoning-delta': {
          appendText(chunk.text)
          break
        }

        case 'start':
        case 'start-step': {
          setState(current => ({
            ...current,
            status: 'running',
            statusMessage: undefined,
          }))
          onStatusChange?.('running')
          break
        }

        case 'text-delta': {
          appendText(chunk.text)
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

function normalizeError(error: unknown): Error {
  if (error instanceof Error) return error
  if (typeof error === 'string') return new Error(error)
  return new Error('Unknown error')
}
