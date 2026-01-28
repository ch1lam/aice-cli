import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import type { ChatMessage, MessageRole } from '../../types/chat.js'
import type { ProviderEnv } from '../../types/env.js'
import type { SelectInputItem } from '../../types/select-input.js'
import type { AppMode, SetupStep } from '../../types/setup-flow.js'
import type { SlashCommandDefinition } from '../../types/slash-commands.js'
import type { SlashSuggestionsState } from '../../types/slash-suggestions-state.js'
import type { ProviderId, StreamStatus, TokenUsage } from '../../types/stream.js'

import { buildMessages as formatMessages } from '../../chat/messages.js'
import { getProviderDefaults, resolveDefaultModel } from '../../config/provider-defaults.js'
import { getProviderModelOptions } from '../../config/provider-models.js'
import { SetupService } from '../../services/setup-service.js'
import { isSlashCommandInput } from '../slash-commands.js'
import { clampIndex, cycleIndex } from '../utils.js'
import { useChatStream, type UseChatStreamOptions } from './use-chat-stream.js'
import { useKeybindings } from './use-keybindings.js'
import { useSetupFlow } from './use-setup-flow.js'
import { useSlashCommands } from './use-slash-commands.js'
import { useSlashSuggestionsState } from './use-slash-suggestions-state.js'

const MAX_HISTORY_MESSAGES = 40

export interface ChatInputControllerResult {
  currentResponse: string
  input: string
  maskInput: boolean
  messages: ChatMessage[]
  mode: AppMode
  modelMenu: {
    active: boolean
    items: Array<SelectInputItem<string>>
    selectedIndex: number
    title: string
  }
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
  onNewSession?: () => void
}

export function useChatInputController(
  options: UseChatInputControllerOptions,
): ChatInputControllerResult {
  const messageId = useRef(0)
  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [modelMenuActive, setModelMenuActive] = useState(false)
  const [modelMenuIndex, setModelMenuIndex] = useState(0)

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

  const buildMessages = useCallback(
    (history: ChatMessage[]) => formatMessages(history, { maxMessages: MAX_HISTORY_MESSAGES }),
    [],
  )

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
    buildMessages,
    createChatService: options.createChatService,
    onAssistantMessage: handleAssistantMessage,
    onSystemMessage: addSystemMessage,
  })

  const {
    cancelSetup,
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
    onEnvReady: env =>
      setSessionMeta({
        model: resolveDefaultModel(env.providerId, env.model),
        providerId: env.providerId,
      }),
    onMessage: addSystemMessage,
    setupService,
  })

  const handleNewCommand = useCallback(() => {
    setMessages([])
    resetSession()
    options.onNewSession?.()
  }, [options.onNewSession, resetSession])

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

  const modelMenuItems = useMemo<Array<SelectInputItem<string>>>(() => {
    if (!providerEnv) return []
    const defaults = getProviderDefaults(providerEnv.providerId)
    const items: Array<SelectInputItem<string>> = [
      {
        description: 'Reset model override.',
        label: 'Default',
        value: defaults.defaultModel,
      },
    ]
    const options = getProviderModelOptions(providerEnv.providerId)

    for (const option of options) {
      if (option.id === defaults.defaultModel) continue
      items.push({
        description: option.description,
        label: option.label,
        value: option.id,
      })
    }

    return items
  }, [providerEnv])

  const modelMenuTitle = useMemo(() => {
    if (!providerEnv) return 'Select model'
    const { label } = getProviderDefaults(providerEnv.providerId)
    return `Select model (${label})`
  }, [providerEnv])

  const closeModelMenu = useCallback(
    (message?: string) => {
      setModelMenuActive(false)
      if (message) addSystemMessage(message)
    },
    [addSystemMessage],
  )

  const confirmModelSelection = useCallback(() => {
    if (!providerEnv) {
      closeModelMenu('Provider not configured. Run /login first.')
      return
    }

    if (modelMenuItems.length === 0) {
      closeModelMenu('No model options available.')
      return
    }

    const defaults = getProviderDefaults(providerEnv.providerId)
    const safeIndex = clampIndex(modelMenuIndex, modelMenuItems.length)
    const selection = modelMenuItems[safeIndex]
    const nextModel = selection.value === defaults.defaultModel ? undefined : selection.value

    try {
      const updatedEnv = setupService.setModel(providerEnv, nextModel)
      setProviderEnv(updatedEnv)
      const resolvedModel = resolveDefaultModel(updatedEnv.providerId, updatedEnv.model)
      setSessionMeta({ model: resolvedModel, providerId: updatedEnv.providerId })
      if (nextModel) {
        addSystemMessage(`Model set to ${selection.value}.`)
      } else {
        addSystemMessage(`Model reset to default (${resolvedModel}).`)
      }
    } catch (persistError) {
      const message = persistError instanceof Error ? persistError.message : String(persistError)
      addSystemMessage(`Failed to save model: ${message}`)
    } finally {
      setModelMenuActive(false)
    }
  }, [
    addSystemMessage,
    closeModelMenu,
    modelMenuIndex,
    modelMenuItems,
    providerEnv,
    setProviderEnv,
    setSessionMeta,
    setupService,
  ])

  const selectNextModel = useCallback(() => {
    setModelMenuIndex(current => cycleIndex(current, 1, modelMenuItems.length))
  }, [modelMenuItems.length])

  const selectPreviousModel = useCallback(() => {
    setModelMenuIndex(current => cycleIndex(current, -1, modelMenuItems.length))
  }, [modelMenuItems.length])

  const handleModelCommand = useCallback(
    (args: string[]) => {
      if (!providerEnv) {
        addSystemMessage('Provider not configured. Run /login first.')
        return
      }

      if (streaming) {
        addSystemMessage('Request already in progress. Please wait.')
        return
      }

      if (args.length > 0) {
        addSystemMessage('Model menu opened. Arguments are ignored.')
      }

      if (modelMenuItems.length === 0) {
        addSystemMessage('No model options available.')
        return
      }

      const currentModel = resolveDefaultModel(providerEnv.providerId, providerEnv.model)
      const currentIndex = modelMenuItems.findIndex(item => item.value === currentModel)
      setModelMenuIndex(Math.max(currentIndex, 0))
      setModelMenuActive(true)
    },
    [addSystemMessage, modelMenuItems, providerEnv, streaming],
  )

  const { handleSlashCommand, suggestions: slashSuggestionsForQuery } = useSlashCommands({
    onEmpty: () => addSystemMessage('Empty command. Use /help to see available commands.'),
    onHelp: handleHelpCommand,
    onLogin: handleLoginCommand,
    onModel: handleModelCommand,
    onNew: handleNewCommand,
    onUnknown: command => addSystemMessage(`Unknown command: /${command ?? ''}`),
  })

  useEffect(() => {
    if (options.initialError) {
      addSystemMessage(`Config error: ${options.initialError.message}`)
    }

    if (options.initialEnv) {
      const resolvedModel = resolveDefaultModel(
        options.initialEnv.providerId,
        options.initialEnv.model,
      )
      addSystemMessage(
        `Ready with ${options.initialEnv.providerId} (${resolvedModel})`,
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
    modelMenu: {
      active: modelMenuActive,
      cancel: () => closeModelMenu('Model selection cancelled.'),
      confirm: confirmModelSelection,
      selectNext: selectNextModel,
      selectPrevious: selectPreviousModel,
    },
    onSetupCancel() {
      setInput('')
      cancelSetup()
    },
    onSubmit: handleSubmit,
    setInput,
    setupMode: mode === 'setup',
    setupSubmitting,
    slashSuggestions,
    streaming,
  })

  const providerMeta = useMemo(() => {
    if (sessionMeta) return sessionMeta

    return providerEnv
      ? {
          model: resolveDefaultModel(providerEnv.providerId, providerEnv.model),
          providerId: providerEnv.providerId,
        }
      : undefined
  }, [providerEnv, sessionMeta])

  return {
    currentResponse,
    input,
    maskInput,
    messages,
    mode,
    modelMenu: {
      active: modelMenuActive,
      items: modelMenuItems,
      selectedIndex: modelMenuIndex,
      title: modelMenuTitle,
    },
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
