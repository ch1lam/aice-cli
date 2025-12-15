import {
  type Dispatch,
  type SetStateAction,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react'

import type { ProviderEnv } from '../../config/env.js'
import type { SessionStream, StreamStatus, TokenUsage } from '../../core/stream.js'
import type { ChatMessage } from '../../domain/chat/index.js'

import { ChatController } from '../../chat/controller.js'
import { type SessionMeta, useSession } from './use-session.js'

export type { ChatMessage, MessageRole } from '../../domain/chat/index.js'

type ChatControllerFactory = (env: ProviderEnv) => Pick<ChatController, 'createStream'>

const defaultCreateController: ChatControllerFactory = env => new ChatController({ env })

export interface UseChatStreamOptions {
  buildPrompt: (history: ChatMessage[]) => string
  createController?: ChatControllerFactory
  onAssistantMessage?: (message: string) => void
  onSystemMessage?: (message: string) => void
}

export interface UseChatStreamResult {
  currentResponse: string
  resetSession(): void
  sessionMeta?: SessionMeta
  sessionStatus?: StreamStatus
  sessionStatusMessage?: string
  sessionUsage?: TokenUsage
  setSessionMeta: Dispatch<SetStateAction<SessionMeta | undefined>>
  startStream(history: ChatMessage[], env: ProviderEnv): void
  streaming: boolean
}

export function useChatStream(options: UseChatStreamOptions): UseChatStreamResult {
  const {
    buildPrompt,
    createController = defaultCreateController,
    onAssistantMessage,
    onSystemMessage,
  } = options

  const [stream, setStream] = useState<SessionStream | undefined>()
  const [sessionMeta, setSessionMeta] = useState<SessionMeta | undefined>()
  const deliveredStreamRef = useRef<SessionStream | undefined>(undefined)

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
  const currentResponse = streaming ? session.content : ''
  const sessionStatus: StreamStatus | undefined = session.status
  const sessionStatusMessage = session.statusMessage
  const sessionUsage: TokenUsage | undefined = session.usage

  useEffect(() => {
    if (session.meta) {
      setSessionMeta(session.meta)
    }
  }, [session.meta])

  useEffect(() => {
    if (!stream || !session.done) return
    if (deliveredStreamRef.current === stream) return
    deliveredStreamRef.current = stream

    if (session.status === 'failed') return
    if (!session.content) return
    onAssistantMessage?.(session.content)
  }, [onAssistantMessage, session.content, session.done, session.status, stream])

  const resetSession = useCallback(() => {
    setStream(undefined)
    deliveredStreamRef.current = undefined
    setSessionMeta(undefined)
  }, [])

  const startStream = useCallback(
    (history: ChatMessage[], env: ProviderEnv) => {
      const prompt = buildPrompt(history)
      const controller = createController(env)

      let nextStream: SessionStream
      try {
        nextStream = controller.createStream({
          model: env.model,
          prompt,
        })
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        onSystemMessage?.(`Failed to start chat: ${message}`)
        return
      }

      setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })
      deliveredStreamRef.current = undefined
      setStream(nextStream)
    },
    [buildPrompt, createController, onSystemMessage],
  )

  return {
    currentResponse,
    resetSession,
    sessionMeta,
    sessionStatus,
    sessionStatusMessage,
    sessionUsage,
    setSessionMeta,
    startStream,
    streaming,
  }
}
