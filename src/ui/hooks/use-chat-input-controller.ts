import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ChatMessage, MessageRole } from '../../types/chat.js'
import type { ProviderEnv } from '../../types/env.js'
import type { AppMode, SetupStep } from '../../types/setup-flow.js'
import type { SlashCommandDefinition } from '../../types/slash-commands.js'
import type { SlashSuggestionsState } from '../../types/slash-suggestions-state.js'
import type { ProviderId, StreamStatus, TokenUsage } from '../../types/stream.js'

import { buildPrompt as formatPrompt } from '../../chat/prompt.js'
import { SetupService } from '../../services/setup-service.js'
import { isSlashCommandInput } from '../slash-commands.js'
import { useChatStream, type UseChatStreamOptions } from './use-chat-stream.js'
import { useKeybindings } from './use-keybindings.js'
import { useSetupFlow } from './use-setup-flow.js'
import { useSlashCommands } from './use-slash-commands.js'
import { useSlashSuggestionsState } from './use-slash-suggestions-state.js'

export interface ChatInputControllerResult {
  currentResponse: string
  input: string
  maskInput: boolean
  messages: ChatMessage[]
  mode: AppMode
  providerMeta?: {model: string; providerId: ProviderId}
  sessionStatus?: StreamStatus
  sessionStatusMessage?: string
  sessionUsage?: TokenUsage
  setupStateStep: SetupStep
  setupSubmitting: boolean
  slashSuggestions: SlashSuggestionsState
  streaming: boolean
}

interface UseChatInputControllerOptions {
  createChatService?: UseChatStreamOptions['createChatService']
  initialEnv?: ProviderEnv
  initialError?: Error
}

export function useChatInputController(
  options: UseChatInputControllerOptions,
): ChatInputControllerResult {
  const messageId = useRef(0)
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])

  const createMessage = useCallback((role: MessageRole, text: string): ChatMessage => {
    const id = messageId.current++
    return { id, role, text }
  }, [])

  const addSystemMessage = useCallback(
    (text: string) => {
      setMessages(current => [...current, createMessage('system', text)])
    },
    [createMessage],
  )

  const setupService = useMemo(() => new SetupService(), [])

  const buildPrompt = useCallback((history: ChatMessage[]) => formatPrompt(history), [])

  const handleAssistantMessage = useCallback(
    (message: string) => {
      setMessages(current => [...current, createMessage('assistant', message)])
    },
    [createMessage],
  )

  const {
    currentResponse,
    resetSession,
    sessionMeta,
    sessionStatus,
    sessionStatusMessage,
    sessionUsage,
    setSessionMeta,
    startStream,
    streaming,
  } = useChatStream({
    buildPrompt,
    createChatService: options.createChatService,
    onAssistantMessage: handleAssistantMessage,
    onSystemMessage: addSystemMessage,
  })

  const {
    handleSetupInput,
    maskInput,
    mode,
    providerEnv,
    resetSetup,
    setProviderEnv,
    setupState,
    setupSubmitting,
  } = useSetupFlow({
    initialEnv: options.initialEnv,
    onEnvReady: env => setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId }),
    onMessage: addSystemMessage,
  })

  const handleClearCommand = useCallback(() => {
    setMessages([])
    resetSession()
    addSystemMessage('Cleared transcript.')
  }, [addSystemMessage, resetSession])

  const handleHelpCommand = useCallback(
    (commandDefinitions: SlashCommandDefinition[]) => {
      const commands = commandDefinitions.map(option => option.usage).join(', ')
      addSystemMessage(`Commands: ${commands}. Type / and use Tab to autocomplete.`)
    },
    [addSystemMessage],
  )

  const handleLoginCommand = useCallback(() => {
    resetSetup()
    addSystemMessage('Restarting setup. Enter API key.')
  }, [addSystemMessage, resetSetup])

  const handleModelCommand = useCallback(
    (args: string[]) => {
      if (!providerEnv) {
        addSystemMessage('Provider not configured. Run /login first.')
        return
      }

      const model = args.join(' ').trim()
      if (!model) {
        addSystemMessage('Usage: /model <model-name>')
        return
      }

      try {
        const updatedEnv = setupService.setModel(providerEnv, model)
        setProviderEnv(updatedEnv)
        setSessionMeta({ model, providerId: updatedEnv.providerId })
        addSystemMessage(`Model set to ${model}.`)
      } catch (persistError) {
        const message = persistError instanceof Error ? persistError.message : String(persistError)
        addSystemMessage(`Failed to save model: ${message}`)
      }
    },
    [addSystemMessage, providerEnv, setProviderEnv, setSessionMeta, setupService],
  )

  const { handleSlashCommand, suggestions: slashSuggestionsForQuery } = useSlashCommands({
    onClear: handleClearCommand,
    onEmpty: () => addSystemMessage('Empty command. Use /help to see available commands.'),
    onHelp: handleHelpCommand,
    onLogin: handleLoginCommand,
    onModel: handleModelCommand,
    onUnknown: command => addSystemMessage(`Unknown command: /${command ?? ''}`),
  })

  useEffect(() => {
    if (options.initialError) {
      addSystemMessage(`Config error: ${options.initialError.message}`)
    }

    if (options.initialEnv) {
      addSystemMessage(
        `Ready with ${options.initialEnv.providerId} (${options.initialEnv.model ?? 'default model'})`,
      )
    } else {
      addSystemMessage('No provider configured. Starting setup... Enter API key.')
    }
  }, [addSystemMessage, options.initialEnv, options.initialError])

  const slashSuggestions = useSlashSuggestionsState({
    input,
    mode,
    suggestionsForQuery: slashSuggestionsForQuery,
  })

  const handleChatInput = useCallback(
    (value: string): void => {
      if (isSlashCommandInput(value)) {
        handleSlashCommand(value)
        return
      }

      if (!providerEnv) {
        addSystemMessage('Provider not configured. Run /login to set up credentials.')
        resetSetup()
        return
      }

      if (streaming) {
        addSystemMessage('Request already in progress. Please wait.')
        return
      }

      const userMessage = createMessage('user', value)
      const history = [...messages, userMessage]
      setMessages(history)
      startStream(history, providerEnv)
    },
    [
      addSystemMessage,
      createMessage,
      handleSlashCommand,
      messages,
      providerEnv,
      resetSetup,
      startStream,
      streaming,
    ],
  )

  const handleSubmit = useCallback(() => {
    const value = input
    const trimmed = value.trim()
    setInput('')

    if (
      mode === 'chat' &&
      slashSuggestions.active &&
      slashSuggestions.suggestions.length > 0 &&
      !streaming
    ) {
      const submission = slashSuggestions.getSubmissionValue(value)
      if (submission.trim()) {
        handleChatInput(submission)
      }

      return
    }

    if (mode === 'setup') {
      handleSetupInput(trimmed)
      return
    }

    if (!trimmed) return
    handleChatInput(value)
  }, [
    handleChatInput,
    handleSetupInput,
    input,
    mode,
    slashSuggestions,
    streaming,
  ])

  useKeybindings({
    input,
    onSubmit: handleSubmit,
    setInput,
    setupSubmitting,
    slashSuggestions,
    streaming,
  })

  const providerMeta = useMemo(() => {
    if (sessionMeta) return sessionMeta

    return providerEnv
      ? { model: providerEnv.model ?? 'default', providerId: providerEnv.providerId }
      : undefined
  }, [providerEnv, sessionMeta])

  return {
    currentResponse,
    input,
    maskInput,
    messages,
    mode,
    providerMeta,
    sessionStatus,
    sessionStatusMessage,
    sessionUsage,
    setupStateStep: setupState.step,
    setupSubmitting,
    slashSuggestions,
    streaming,
  }
}
