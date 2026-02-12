import type { ModelMessage } from 'ai'

import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react'

import type { ChatMessage } from '../../types/chat.js'
import type { ProviderEnv } from '../../types/env.js'
import type { SessionMeta } from '../../types/session-meta.js'
import type { SessionStream, StreamStatus, TokenUsage } from '../../types/stream.js'

import { resolveDefaultModel } from '../../config/provider-defaults.js'
import { ChatService } from '../../services/chat-service.js'
import { type SessionStreamEvent, useSession } from './use-session.js'

export type { ChatMessage, MessageRole } from '../../types/chat.js'

type ChatServiceFactory = () => Pick<ChatService, 'createStream'>

const defaultCreateChatService: ChatServiceFactory = () => new ChatService()

export interface UseChatStreamOptions {
  buildMessages: (history: ChatMessage[]) => ModelMessage[]
  createChatService?: ChatServiceFactory
  onAssistantMessage?: (message: string) => void
  onSystemMessage?: (message: string) => void
}

export interface UseChatStreamResult {
  currentResponse: string
  progressMessages: string[]
  resetSession(): void
  sessionMeta?: SessionMeta
  sessionStatus?: StreamStatus
  sessionStatusMessage?: string
  sessionUsage?: TokenUsage
  setSessionMeta: Dispatch<SetStateAction<SessionMeta | undefined>>
  startStream(history: ChatMessage[], env: ProviderEnv): void
  streamEvents: SessionStreamEvent[]
  streaming: boolean
}

export function useChatStream(options: UseChatStreamOptions): UseChatStreamResult {
  const {
    buildMessages,
    createChatService = defaultCreateChatService,
    onAssistantMessage,
    onSystemMessage,
  } = options

  const [stream, setStream] = useState<SessionStream | undefined>()
  const [sessionMeta, setSessionMeta] = useState<SessionMeta | undefined>()
  const deliveredStreamRef = useRef<SessionStream | undefined>(undefined)
  const latestContentRef = useRef('')

  const handleSessionError = useCallback(
    (error: Error) => {
      onSystemMessage?.(`Provider error: ${error.message}`)
    },
    [onSystemMessage],
  )

  const session = useSession({
    onError: handleSessionError,
    stream,
  })

  const streaming = Boolean(stream) && !session.done
  const currentResponse = session.content || latestContentRef.current
  const {
    progressMessages,
    status: sessionStatus,
    statusMessage: sessionStatusMessage,
    streamEvents,
    usage: sessionUsage,
  } = session

  useEffect(() => {
    if (session.content === '' && latestContentRef.current) return
    latestContentRef.current = session.content
  }, [session.content])

  useEffect(() => {
    if (!stream || !session.done) return
    if (deliveredStreamRef.current === stream) return
    deliveredStreamRef.current = stream

    if (session.status === 'failed') return
    if (!latestContentRef.current) return
    onAssistantMessage?.(latestContentRef.current)
  }, [onAssistantMessage, session.content, session.done, session.status, stream])

  const resetSession = useCallback(() => {
    setStream(undefined)
    deliveredStreamRef.current = undefined
    latestContentRef.current = ''
    setSessionMeta(undefined)
  }, [])

  const startStream = useCallback(
    (history: ChatMessage[], env: ProviderEnv) => {
      const messages = buildMessages(history)
      const chatService = createChatService()

      let nextStream: SessionStream
      try {
        nextStream = chatService.createStream(env, {
          messages,
          model: env.model,
        })
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        onSystemMessage?.(`Failed to start agent: ${message}`)
        return
      }

      setSessionMeta({
        model: resolveDefaultModel(env.providerId, env.model),
        providerId: env.providerId,
      })
      latestContentRef.current = ''
      setStream(nextStream)
    },
    [buildMessages, createChatService, onSystemMessage],
  )

  return {
    currentResponse,
    progressMessages,
    resetSession,
    sessionMeta,
    sessionStatus,
    sessionStatusMessage,
    sessionUsage,
    setSessionMeta,
    startStream,
    streamEvents,
    streaming,
  }
}
