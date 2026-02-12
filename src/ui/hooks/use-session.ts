import { useEffect, useRef, useState } from 'react'

import type { ProviderStreamChunk, SessionStream, StreamStatus, TokenUsage } from '../../types/stream.js'

export type SessionStreamEvent =
  | { kind: 'assistant'; text: string }
  | { kind: 'progress'; text: string }

export interface SessionState {
  content: string
  done: boolean
  error?: Error
  progressMessages: string[]
  status?: StreamStatus
  statusMessage?: string
  streamEvents: SessionStreamEvent[]
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
    let progressMessages: string[] = []
    let streamEvents: SessionStreamEvent[] = []
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
            streamEvents,
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
            progressMessages,
            status: 'failed',
            statusMessage: error.message,
            streamEvents,
          }))
          onError?.(error)
          onStatusChange?.('failed')
        }
      }
    }

    function appendText(text: string) {
      content += text
      streamEvents = appendAssistantEvent(streamEvents, text)
      setState(current => ({
        ...current,
        content,
        streamEvents,
      }))
    }

    function appendProgressMessage(message: string) {
      progressMessages = [...progressMessages, message]
      streamEvents = [...streamEvents, { kind: 'progress', text: message }]

      setState(current => ({
        ...current,
        progressMessages,
        status: 'running',
        statusMessage: message,
        streamEvents,
      }))
      onStatusChange?.('running')
    }

    function consumeChunk(chunk: ProviderStreamChunk) {
      const progressMessage = resolveProgressMessage(chunk)
      if (progressMessage) {
        appendProgressMessage(progressMessage)
        return
      }

      switch (chunk.type) {
        case 'abort': {
          sawDone = true
          setState(current => ({
            ...current,
            content,
            done: true,
            progressMessages,
            status: 'aborted',
            statusMessage: 'Aborted',
            streamEvents,
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
            progressMessages,
            status: 'failed',
            statusMessage: error.message,
            streamEvents,
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
            progressMessages,
            status: 'completed',
            statusMessage: chunk.finishReason === 'stop' ? undefined : chunk.finishReason,
            streamEvents,
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

        case 'reasoning-delta': {
          appendText(chunk.text)
          break
        }

        case 'start':
        case 'start-step': {
          setState(current => ({
            ...current,
            status: 'running',
            statusMessage: current.statusMessage,
          }))
          onStatusChange?.('running')
          break
        }

        case 'text-delta': {
          appendText(chunk.text)
          break
        }

        default: {
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
    progressMessages: [],
    status: active ? 'running' : undefined,
    statusMessage: undefined,
    streamEvents: [],
    usage: undefined,
  }
}

function normalizeError(error: unknown): Error {
  if (error instanceof Error) return error
  if (typeof error === 'string') return new Error(error)
  return new Error('Unknown error')
}

function formatToolApprovalRequestMessage(toolName: string): string {
  return `Waiting for approval: ${toolName}.`
}

function resolveProgressMessage(chunk: ProviderStreamChunk): string | undefined {
  if (chunk.type === 'tool-approval-request') {
    return formatToolApprovalRequestMessage(chunk.toolCall.toolName)
  }

  if (chunk.type === 'tool-call') {
    return formatToolCallMessage(chunk.toolName, chunk.input)
  }

  if (chunk.type === 'tool-error') {
    return formatToolErrorMessage(chunk.toolName, chunk.error)
  }

  if (chunk.type === 'tool-output-denied') {
    return formatToolOutputDeniedMessage(chunk.toolName)
  }

  if (chunk.type === 'tool-result') {
    return formatToolResultMessage(chunk.toolName, chunk.input)
  }

  return undefined
}

function formatToolCallMessage(toolName: string, input: unknown): string {
  if (toolName === 'list_files') {
    const path = readStringField(input, 'path') ?? '.'
    return `Reading directory ${path}.`
  }

  if (toolName === 'read_file') {
    const path = readStringField(input, 'path') ?? '(unknown file)'
    return `Reading file ${path}.`
  }

  if (toolName === 'search_files') {
    const query = readStringField(input, 'query')
    const path = readStringField(input, 'path') ?? '.'
    if (!query) {
      return `Searching files in ${path}.`
    }

    return `Searching "${truncate(query, 48)}" in ${path}.`
  }

  if (toolName === 'get_current_time') {
    return 'Checking current time.'
  }

  return `Running tool ${toolName}.`
}

function formatToolErrorMessage(toolName: string, error: unknown): string {
  return `Tool ${toolName} failed: ${normalizeError(error).message}`
}

function formatToolOutputDeniedMessage(toolName: string): string {
  return `Tool output denied: ${toolName}.`
}

function formatToolResultMessage(toolName: string, input: unknown): string {
  if (toolName === 'list_files') {
    const path = readStringField(input, 'path') ?? '.'
    return `Finished directory scan for ${path}.`
  }

  if (toolName === 'read_file') {
    const path = readStringField(input, 'path') ?? '(unknown file)'
    return `Finished reading ${path}.`
  }

  if (toolName === 'search_files') {
    const query = readStringField(input, 'query')
    if (!query) {
      return 'Finished file search.'
    }

    return `Finished search for "${truncate(query, 48)}".`
  }

  if (toolName === 'get_current_time') {
    return 'Finished time check.'
  }

  return `Completed tool ${toolName}.`
}

function readStringField(value: unknown, field: string): string | undefined {
  if (!value || typeof value !== 'object') return undefined
  if (!(field in value)) return undefined

  const record = value as Record<string, unknown>
  const fieldValue = record[field]
  if (typeof fieldValue !== 'string') return undefined
  const trimmed = fieldValue.trim()
  return trimmed.length > 0 ? trimmed : undefined
}

function truncate(value: string, maxLength: number): string {
  if (value.length <= maxLength) return value
  if (maxLength <= 3) return value.slice(0, maxLength)
  return `${value.slice(0, maxLength - 3)}...`
}

function appendAssistantEvent(
  events: SessionStreamEvent[],
  text: string,
): SessionStreamEvent[] {
  if (!text) return events
  const last = events.at(-1)

  if (last?.kind === 'assistant') {
    return [...events.slice(0, -1), { kind: 'assistant', text: `${last.text}${text}` }]
  }

  return [...events, { kind: 'assistant', text }]
}
