import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ProviderId, StreamStatus, TokenUsage } from '../../core/stream.js'
import type { ChatMessage, MessageRole } from '../../types/chat.js'
import type { ProviderEnv } from '../../types/env.js'
import type { AppMode, SetupStep } from '../../types/setup-flow.js'
import type { SlashCommandDefinition } from '../../types/slash-commands.js'
import type { SlashSuggestionsState } from '../../types/slash-suggestions-state.js'

import { buildPrompt as formatPrompt } from '../../chat/prompt.js'
import { DEFAULT_PROVIDER_ID } from '../../config/provider-defaults.js'
import { ProviderNotConfiguredError, SetupService } from '../../services/setup-service.js'
import { providerOptionIndex, providerOptions } from '../provider-options.js'
import { isSlashCommandInput } from '../slash-commands.js'
import { useChatStream } from './use-chat-stream.js'
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
  providerChoiceIndex: number
  providerMeta?: {model: string; providerId: ProviderId}
  providerSelection: ProviderId
  sessionStatus?: StreamStatus
  sessionStatusMessage?: string
  sessionUsage?: TokenUsage
  setupStateStep: SetupStep
  setupSubmitting: boolean
  slashSuggestions: SlashSuggestionsState
  streaming: boolean
}

interface UseChatInputControllerOptions {
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
    onAssistantMessage: message =>
      setMessages(current => [...current, createMessage('assistant', message)]),
    onSystemMessage: addSystemMessage,
  })

  const {
    handleSetupInput,
    maskInput,
    mode,
    providerChoiceIndex,
    providerEnv,
    providerSelection,
    resetSetup,
    setProviderChoiceIndex,
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
    const nextProviderId = providerEnv?.providerId ?? providerSelection
    resetSetup(nextProviderId)
    const providerList = providerOptions.length > 0
      ? providerOptions.map(option => option.value).join('/')
      : DEFAULT_PROVIDER_ID
    addSystemMessage(
      `Restarting setup. Use arrow keys to choose provider (${providerList}).`,
    )
  }, [addSystemMessage, providerEnv?.providerId, providerSelection, resetSetup])

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

  const handleProviderCommand = useCallback(
    (args: string[]) => {
      const rawProviderId = args[0]?.trim() ?? ''
      const providerId = providerOptions.find(option => option.value === rawProviderId)?.value
      if (!providerId) {
        const usage = providerOptions.length > 0
          ? providerOptions.map(option => option.value).join('|')
          : DEFAULT_PROVIDER_ID
        addSystemMessage(`Usage: /provider <${usage}>`)
        return
      }

      try {
        const env = setupService.switchProvider(providerId)

        setProviderEnv(env)
        setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })
        setProviderChoiceIndex(providerOptionIndex(providerId))
        addSystemMessage(`Switched to ${providerId} (${env.model ?? 'default model'}).`)
      } catch (persistError) {
        if (persistError instanceof ProviderNotConfiguredError) {
          addSystemMessage(
            `Provider ${providerId} is not configured. Run /login to set API key first.`,
          )
          return
        }

        const message = persistError instanceof Error ? persistError.message : String(persistError)
        addSystemMessage(`Failed to switch provider: ${message}`)
      }
    },
    [
      addSystemMessage,
      setProviderChoiceIndex,
      setProviderEnv,
      setSessionMeta,
      setupService,
    ],
  )

  const { handleSlashCommand, suggestions: slashSuggestionsForQuery } = useSlashCommands({
    onClear: handleClearCommand,
    onEmpty: () => addSystemMessage('Empty command. Use /help to see available commands.'),
    onHelp: handleHelpCommand,
    onLogin: handleLoginCommand,
    onModel: handleModelCommand,
    onProvider: handleProviderCommand,
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
      addSystemMessage('No provider configured. Starting setup... Use arrow keys to pick a provider.')
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
      setMessages(current => {
        const history = [...current, userMessage]
        startStream(history, providerEnv)
        return history
      })
    },
    [
      addSystemMessage,
      createMessage,
      handleSlashCommand,
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
      const setupValue =
        setupState.step === 'provider'
          ? providerOptions[providerChoiceIndex]?.value ?? DEFAULT_PROVIDER_ID
          : trimmed

      handleSetupInput(setupValue)
      return
    }

    if (!trimmed) return
    handleChatInput(value)
  }, [
    handleChatInput,
    handleSetupInput,
    input,
    mode,
    providerChoiceIndex,
    setupState.step,
    slashSuggestions,
    streaming,
  ])

  useKeybindings({
    input,
    mode,
    onSubmit: handleSubmit,
    providerOptionCount: providerOptions.length,
    setInput,
    setProviderChoiceIndex,
    setupState,
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
    providerChoiceIndex,
    providerMeta,
    providerSelection,
    sessionStatus,
    sessionStatusMessage,
    sessionUsage,
    setupStateStep: setupState.step,
    setupSubmitting,
    slashSuggestions,
    streaming,
  }
}
