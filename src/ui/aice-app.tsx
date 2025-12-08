import {Box, Text, useApp, useInput} from 'ink'
import {useCallback, useEffect, useMemo, useRef, useState} from 'react'

import type {ProviderEnv} from '../config/env.js'
import type {ProviderId} from '../core/stream.js'
import type {ChatMessage, MessageRole} from './hooks/use-chat-stream.js'
import type {AppMode, SetupStep} from './hooks/use-setup-flow.js'
import type {SlashCommandDefinition} from './slash-commands.js'
import type {SlashSuggestion} from './slash-suggestions.js'

import {persistProviderEnv, tryLoadProviderEnv} from '../config/env.js'
import {useChatStream} from './hooks/use-chat-stream.js'
import {useSetupFlow} from './hooks/use-setup-flow.js'
import {useSlashCommands} from './hooks/use-slash-commands.js'
import {InputPanel} from './input-panel.js'
import {providerOptionIndex, providerOptions} from './provider-options.js'
import {SelectInput} from './select-input.js'
import {isSlashCommandInput} from './slash-commands.js'
import {SlashSuggestions} from './slash-suggestions.js'
import {StatusBar} from './status-bar.js'
import {theme} from './theme.js'

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

const messageColors = theme.components.messages

export function AiceApp(props: AiceAppProps) {
  const {exit} = useApp()
  const messageId = useRef(0)

  const [input, setInput] = useState('')
  const [messages, setMessages] = useState<ChatMessage[]>([])
  const [slashSuggestionIndex, setSlashSuggestionIndex] = useState(0)
  const [cursorVisible, setCursorVisible] = useState(true)

  const createMessage = useCallback((role: MessageRole, text: string): ChatMessage => {
    const id = messageId.current++
    return {id, role, text}
  }, [])

  const addSystemMessage = useCallback(
    (text: string) => {
      setMessages(current => [...current, createMessage('system', text)])
    },
    [createMessage],
  )

  const buildPrompt = useCallback((history: ChatMessage[]) => buildPromptFromHistory(history), [])

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
    initialEnv: props.initialEnv,
    onEnvReady: env => setSessionMeta(createMetaFromEnv(env)),
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
    addSystemMessage(
      'Restarting setup. Use arrow keys to choose provider (openai/openai-agents/anthropic/deepseek).',
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
        persistProviderEnv({
          apiKey: providerEnv.apiKey,
          baseURL: providerEnv.baseURL,
          instructions: providerEnv.instructions,
          model,
          providerId: providerEnv.providerId,
        })
      } catch (persistError) {
        const message = persistError instanceof Error ? persistError.message : String(persistError)
        addSystemMessage(`Failed to save model: ${message}`)
        return
      }

      setProviderEnv(current => (current ? {...current, model} : current))
      setSessionMeta({model, providerId: providerEnv.providerId})
      addSystemMessage(`Model set to ${model}.`)
    },
    [addSystemMessage, providerEnv, setProviderEnv, setSessionMeta],
  )

  const handleProviderCommand = useCallback(
    (args: string[]) => {
      const providerId = parseProviderId(args[0] ?? '')
      if (!providerId) {
        addSystemMessage('Usage: /provider <openai|openai-agents|anthropic|deepseek>')
        return
      }

      const {env, error} = tryLoadProviderEnv({providerId})
      if (!env || error) {
        addSystemMessage(`Provider ${providerId} is not configured. Run /login to set API key first.`)
        return
      }

      try {
        persistProviderEnv({
          apiKey: env.apiKey,
          baseURL: env.baseURL,
          instructions: env.instructions,
          model: env.model,
          providerId,
        })
      } catch (persistError) {
        const message = persistError instanceof Error ? persistError.message : String(persistError)
        addSystemMessage(`Failed to switch provider: ${message}`)
        return
      }

      setProviderEnv(env)
      setSessionMeta(createMetaFromEnv(env))
      setProviderChoiceIndex(providerOptionIndex(providerId))
      addSystemMessage(`Switched to ${providerId} (${env.model ?? 'default model'}).`)
    },
    [addSystemMessage, setProviderChoiceIndex, setProviderEnv, setSessionMeta],
  )

  const {handleSlashCommand, suggestions: slashSuggestionsForQuery} = useSlashCommands({
    onClear: handleClearCommand,
    onEmpty: () => addSystemMessage('Empty command. Use /help to see available commands.'),
    onHelp: handleHelpCommand,
    onLogin: handleLoginCommand,
    onModel: handleModelCommand,
    onProvider: handleProviderCommand,
    onUnknown: command => addSystemMessage(`Unknown command: /${command ?? ''}`),
  })

  useEffect(() => {
    if (props.initialError) {
      addSystemMessage(`Config error: ${props.initialError.message}`)
    }

    if (props.initialEnv) {
      addSystemMessage(
        `Ready with ${props.initialEnv.providerId} (${props.initialEnv.model ?? 'default model'})`,
      )
    } else {
      addSystemMessage('No provider configured. Starting setup... Use arrow keys to pick a provider.')
    }
  }, [addSystemMessage, props.initialEnv, props.initialError])

  useEffect(() => {
    const intervalId = setInterval(() => {
      setCursorVisible(current => !current)
    }, 500)

    return () => {
      clearInterval(intervalId)
    }
  }, [])

  const slashCommandActive = mode === 'chat' && isSlashCommandInput(input)

  const slashQuery = useMemo(() => {
    if (!slashCommandActive) return ''
    const [commandPart] = input.slice(1).split(' ')
    return (commandPart ?? '').toLowerCase()
  }, [input, slashCommandActive])

  const slashSuggestions: SlashSuggestion[] = slashCommandActive
    ? slashSuggestionsForQuery(slashQuery)
    : []

  useEffect(() => {
    if (!slashCommandActive || slashSuggestions.length === 0) {
      setSlashSuggestionIndex(0)
      return
    }

    setSlashSuggestionIndex(current => clampIndex(current, slashSuggestions.length))
  }, [slashCommandActive, slashSuggestions.length])

  useEffect(() => {
    if (!slashCommandActive) return
    setSlashSuggestionIndex(0)
  }, [slashCommandActive, slashQuery])

  const trySubmitSlash = useCallback(
    (rawValue: string): boolean => {
      if (mode !== 'chat' || !slashCommandActive || slashSuggestions.length === 0 || streaming) {
        return false
      }

      const submission = buildSlashSubmissionValue(slashSuggestionIndex, rawValue, slashSuggestions)
      if (submission.trim()) {
        handleChatInput(submission)
      }

      return true
    },
    [handleChatInput, mode, slashCommandActive, slashSuggestionIndex, slashSuggestions, streaming],
  )

  const trySubmitSetup = useCallback(
    (trimmedValue: string): boolean => {
      if (mode !== 'setup') return false

      const setupValue =
        setupState.step === 'provider'
          ? providerOptions[providerChoiceIndex]?.value ?? 'openai'
          : trimmedValue

      handleSetupInput(setupValue)
      return true
    },
    [handleSetupInput, mode, providerChoiceIndex, setupState.step],
  )

  const handleSubmit = useCallback(() => {
    const value = input
    const trimmed = value.trim()
    setInput('')

    if (trySubmitSlash(value)) return
    if (trySubmitSetup(trimmed)) return
    if (!trimmed) return

    handleChatInput(value)
  }, [handleChatInput, input, trySubmitSetup, trySubmitSlash])

  function handleProviderChoiceInput(key: {downArrow?: boolean; return?: boolean; upArrow?: boolean}): boolean {
    if (key.upArrow) {
      setProviderChoiceIndex(current => cycleProviderChoice(current, -1))
      return true
    }

    if (key.downArrow) {
      setProviderChoiceIndex(current => cycleProviderChoice(current, 1))
      return true
    }

    if (key.return) {
      handleSubmit()
      return true
    }

    return false
  }

  function handleSlashSuggestionInput(
    key: {downArrow?: boolean; tab?: boolean; upArrow?: boolean},
    hasSlashSuggestions: boolean,
    currentInput: string,
  ): boolean {
    if (!hasSlashSuggestions) return false

    if (key.tab) {
      applySlashSuggestion(slashSuggestionIndex, slashSuggestions, currentInput, setInput)
      return true
    }

    if (key.downArrow) {
      setSlashSuggestionIndex(current => cycleIndex(current, 1, slashSuggestions.length))
      return true
    }

    if (key.upArrow) {
      setSlashSuggestionIndex(current => cycleIndex(current, -1, slashSuggestions.length))
      return true
    }

    return false
  }

  useInput((receivedInput, key) => {
    if (key.ctrl && receivedInput === 'c') {
      exit()
      return
    }

    if (setupSubmitting) return

    if (mode === 'setup' && setupState.step === 'provider') {
      if (handleProviderChoiceInput(key)) return
      return
    }

    const hasSlashSuggestions = slashCommandActive && slashSuggestions.length > 0 && !streaming
    if (handleSlashSuggestionInput(key, hasSlashSuggestions, input)) return

    if (key.return) {
      handleSubmit()
      return
    }

    if (key.backspace || key.delete) {
      setInput(current => current.slice(0, -1))
      return
    }

    if (key.escape) return

    if (receivedInput) {
      setInput(current => `${current}${receivedInput}`)
    }
  })

  function handleChatInput(value: string): void {
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
  }

  const inputLabel = '>'
  const renderedInput = maskInput ? '*'.repeat(input.length) : input
  const providerMeta = resolveProviderMeta(sessionMeta, providerEnv)
  const providerPrompt = providerOptions[providerChoiceIndex]?.value ?? providerSelection
  const hint = resolveHint(mode, setupState.step, providerPrompt)
  const placeholder = resolvePlaceholder(streaming, setupSubmitting, hint)
  const showCursor = !streaming && !setupSubmitting && cursorVisible
  const showProviderSelect = isProviderSelectVisible(mode, setupState.step)
  const showSlashSuggestions = shouldShowSlashSuggestions(
    mode,
    streaming,
    slashCommandActive,
    slashSuggestions.length,
  )

  return (
    <Box flexDirection="column">
      <Text color={theme.components.app.title}>AICE</Text>
      <Box flexDirection="column" marginBottom={1}>
        {messages.map(message => (
          <Text color={colorForRole(message.role)} key={message.id} wrap="wrap">
            {`${labelForRole(message.role)} ${message.text}`}
          </Text>
        ))}
        {streaming ? (
          <Text color={messageColors.assistant} wrap="wrap">
            {`Assistant: ${currentResponse || '...'}${sessionStatus === 'completed' || !cursorVisible ? '  ' : ' ▌'}`}
          </Text>
        ) : null}
      </Box>
      <Box flexDirection="column" width="100%">
        {showProviderSelect ? (
          <SelectInput
            active
            items={providerOptions}
            selectedIndex={providerChoiceIndex}
            title="Choose a provider"
          />
        ) : (
          <>
            <InputPanel
              cursorVisible={showCursor}
              disabled={streaming || setupSubmitting}
              label={inputLabel}
              placeholder={placeholder}
              value={renderedInput}
            />
            <SlashSuggestions
              activeIndex={slashSuggestionIndex}
              items={slashSuggestions}
              visible={showSlashSuggestions}
            />
          </>
        )}
      </Box>
      <Box width="100%">
        <StatusBar
          meta={providerMeta}
          status={sessionStatus}
          statusMessage={sessionStatusMessage}
          usage={sessionUsage}
        />
      </Box>
    </Box>
  )
}

function cycleProviderChoice(current: number, delta: number): number {
  const total = providerOptions.length
  if (total === 0) return 0
  return (current + delta + total) % total
}

function cycleIndex(current: number, delta: number, length: number): number {
  if (length === 0) return 0
  return (current + delta + length) % length
}

function clampIndex(index: number, length: number): number {
  if (length === 0) return 0
  if (index < 0) return 0
  if (index >= length) return length - 1
  return index
}

function parseProviderId(value: string): ProviderId | undefined {
  if (
    value === 'openai' ||
    value === 'openai-agents' ||
    value === 'anthropic' ||
    value === 'deepseek'
  ) {
    return value
  }

  return undefined
}

function colorForRole(role: MessageRole): string {
  if (role === 'assistant') return messageColors.assistant
  if (role === 'user') return messageColors.user
  return messageColors.system
}

function labelForRole(role: MessageRole): string {
  if (role === 'assistant') return 'Assistant:'
  if (role === 'user') return 'You:'
  return 'System:'
}

function buildPromptFromHistory(history: ChatMessage[]): string {
  const exchanges = history
    .filter(message => message.role !== 'system')
    .map(message => `${message.role === 'user' ? 'User' : 'Assistant'}: ${message.text}`)

  exchanges.push('Assistant:')

  return exchanges.join('\n')
}

function createMetaFromEnv(env: ProviderEnv): {model: string; providerId: ProviderId} {
  return {
    model: env.model ?? 'default',
    providerId: env.providerId,
  }
}

function setupPrompt(step: SetupStep, providerId: ProviderId = 'openai'): string {
  if (step === 'provider') {
    return `Use arrow keys to choose provider (current: ${providerId}). Press Enter to confirm.`
  }

  if (step === 'apiKey') {
    return 'Enter API key (hidden as you type).'
  }

  if (step === 'baseURL') {
    return 'Optional: enter a base URL override, or press Enter to use the default.'
  }

  if (step === 'model') {
    return 'Optional: enter model override, or press Enter to use the default.'
  }

  if (step === 'instructions') {
    return 'Optional: enter default agent instructions, or press Enter to use the default.'
  }

  return ''
}

function applySlashSuggestion(
  targetIndex: number,
  suggestions: SlashSuggestion[],
  currentInput: string,
  setInput: (value: string) => void,
): void {
  const nextValue = buildSlashSubmissionValue(targetIndex, currentInput, suggestions)
  if (!nextValue) return
  const needsSpace = nextValue.endsWith(' ') ? '' : ' '
  setInput(`${nextValue}${needsSpace}`)
}

function buildSlashSubmissionValue(
  targetIndex: number,
  baseInput: string,
  suggestions: SlashSuggestion[],
): string {
  const suggestion = suggestions[clampIndex(targetIndex, suggestions.length)]
  if (!suggestion) return baseInput.trim()

  const [, ...args] = baseInput.slice(1).split(' ')
  const argsText = args.join(' ').trim()
  const argSegment = argsText ? ` ${argsText}` : ''
  return `/${suggestion.command}${argSegment}`.trim()
}

function resolveProviderMeta(
  sessionMeta: undefined | {model: string; providerId: ProviderId},
  providerEnv: ProviderEnv | undefined,
): undefined | {model: string; providerId: ProviderId} {
  return sessionMeta ?? (providerEnv ? createMetaFromEnv(providerEnv) : undefined)
}

function resolveHint(mode: AppMode, step: SetupStep, providerId: ProviderId): string {
  if (mode === 'setup') {
    return setupPrompt(step, providerId)
  }

  return 'Type a prompt or use /help, /login, /provider, /model, /clear'
}

function resolvePlaceholder(streaming: boolean, setupSubmitting: boolean, hint: string): string {
  if (streaming) return 'Processing response...'
  if (setupSubmitting) return 'Validating provider...'
  return hint
}

function isProviderSelectVisible(mode: AppMode, step: SetupStep): boolean {
  return mode === 'setup' && step === 'provider'
}

function shouldShowSlashSuggestions(
  mode: AppMode,
  streaming: boolean,
  slashCommandActive: boolean,
  suggestionCount: number,
): boolean {
  return !streaming && mode === 'chat' && slashCommandActive && suggestionCount > 0
}
