import { type Dispatch, type SetStateAction, useCallback, useState } from 'react'

import type { ChatMessage } from '../../chat/prompt.js'
import type { ProviderEnv } from '../../config/env.js'
import type { SessionStream, StreamStatus, TokenUsage } from '../../core/stream.js'
import type { SessionMeta } from './use-session.js'

import { ChatController } from '../../chat/controller.js'

export type { ChatMessage, MessageRole } from '../../chat/prompt.js'

type ChatControllerFactory = (env: ProviderEnv) => Pick<ChatController, 'createStream'>

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
    createController = env => new ChatController({ env }),
    onAssistantMessage,
    onSystemMessage,
  } = options

  const [sessionMeta, setSessionMeta] = useState<SessionMeta | undefined>()
  const [sessionStatus, setSessionStatus] = useState<StreamStatus | undefined>()
  const [sessionStatusMessage, setSessionStatusMessage] = useState<string | undefined>()
  const [sessionUsage, setSessionUsage] = useState<TokenUsage | undefined>()
  const [currentResponse, setCurrentResponse] = useState('')
  const [streaming, setStreaming] = useState(false)

  const resetSession = useCallback(() => {
    setCurrentResponse('')
    setSessionMeta(undefined)
    setSessionStatus(undefined)
    setSessionStatusMessage(undefined)
    setSessionUsage(undefined)
  }, [])

  const startStream = useCallback(
    (history: ChatMessage[], env: ProviderEnv) => {
      const prompt = buildPrompt(history)
      const controller = createController(env)

      let stream: SessionStream
      try {
        stream = controller.createStream({
          model: env.model,
          prompt,
          providerId: env.providerId,
        })
      } catch (error) {
        const message = error instanceof Error ? error.message : String(error)
        onSystemMessage?.(`Failed to start chat: ${message}`)
        return
      }

      setStreaming(true)
      setCurrentResponse('')
      setSessionStatus('running')
      setSessionStatusMessage(undefined)
      setSessionUsage(undefined)
      setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })

      let buffer = ''

      function handleStreamError(error: unknown) {
        const message = error instanceof Error ? error.message : String(error)
        setSessionStatus('failed')
        setSessionStatusMessage(message)
        onSystemMessage?.(`Provider error: ${message}`)
      }

      async function streamChunks() {
        try {
          for await (const chunk of stream) {
            switch (chunk.type) {
              case 'done': {
                setSessionStatus('completed')
                setSessionStatusMessage(undefined)
                break
              }

              case 'error': {
                handleStreamError(chunk.error)
                return
              }

              case 'meta': {
                setSessionMeta({ model: chunk.model, providerId: chunk.providerId })
                break
              }

              case 'status': {
                setSessionStatus(chunk.status)
                setSessionStatusMessage(chunk.detail)
                break
              }

              case 'text': {
                buffer += chunk.text
                setCurrentResponse(buffer)
                break
              }

              case 'usage': {
                setSessionUsage(chunk.usage)
                break
              }
            }
          }

          if (buffer) {
            onAssistantMessage?.(buffer)
          }
        } catch (error) {
          handleStreamError(error)
        } finally {
          setStreaming(false)
          setCurrentResponse('')
        }
      }

      streamChunks()
    },
    [buildPrompt, createController, onAssistantMessage, onSystemMessage],
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
