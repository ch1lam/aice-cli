import {Box, Text, useApp, useInput} from 'ink'
import {useEffect, useMemo, useRef, useState} from 'react'

import type {ProviderId, SessionStream, StreamStatus, TokenUsage} from '../core/stream.js'

import {ChatController} from '../chat/controller.js'
import {persistProviderEnv, type ProviderEnv, tryLoadProviderEnv} from '../config/env.js'
import {pingProvider} from '../providers/ping.js'
import {InputPanel} from './input-panel.js'
import {SelectInput, type SelectInputItem} from './select-input.js'
import {type SlashSuggestion, SlashSuggestions} from './slash-suggestions.js'
import {StatusBar} from './status-bar.js'
import {theme} from './theme.js'

type Mode = 'chat' | 'setup'

type SetupStep = 'apiKey' | 'baseURL' | 'instructions' | 'model' | 'provider'

type MessageRole = 'assistant' | 'system' | 'user'

type SlashHandler = (args: string[]) => void

type ProviderOption = SelectInputItem<ProviderId>

const providerOptions: ProviderOption[] = [
  {description: 'Responses API (default)', label: 'OpenAI', value: 'openai'},
  {description: 'Agents API', label: 'OpenAI Agents', value: 'openai-agents'},
  {description: 'Claude 3.7 and newer', label: 'Anthropic', value: 'anthropic'},
  {description: 'DeepSeek chat + reasoning', label: 'DeepSeek', value: 'deepseek'},
]

const slashCommandList = [
  {command: 'help', description: 'Show available commands and usage.', hint: '/help'},
  {command: 'login', description: 'Restart setup and enter a provider API key.', hint: '/login'},
  {command: 'provider', description: 'Switch between configured providers.', hint: '/provider openai'},
  {command: 'model', description: 'Set or change the active model override.', hint: '/model gpt-4o-mini'},
  {command: 'clear', description: 'Clear the transcript.', hint: '/clear'},
] as const

type SlashCommandId = (typeof slashCommandList)[number]['command']

interface Message {
  id: number
  role: MessageRole
  text: string
}

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

const messageColors = theme.components.messages

interface SetupState {
  apiKey?: string
  baseURL?: string
  instructions?: string
  model?: string
  providerId: ProviderId
  step: SetupStep
}

export function AiceApp(props: AiceAppProps) {
  const {exit} = useApp()
  const initialProviderId = props.initialEnv?.providerId ?? 'openai'

  const [mode, setMode] = useState<Mode>(props.initialEnv ? 'chat' : 'setup')
  const [input, setInput] = useState('')
  const [maskInput, setMaskInput] = useState(false)
  const [messages, setMessages] = useState<Message[]>([])
  const [providerEnv, setProviderEnv] = useState<ProviderEnv | undefined>(props.initialEnv)
  const [sessionMeta, setSessionMeta] = useState<{ model: string; providerId: ProviderId }>()
  const [sessionStatus, setSessionStatus] = useState<StreamStatus | undefined>()
  const [sessionStatusMessage, setSessionStatusMessage] = useState<string | undefined>()
  const [sessionUsage, setSessionUsage] = useState<TokenUsage | undefined>()
  const [streaming, setStreaming] = useState(false)
  const [currentResponse, setCurrentResponse] = useState('')
  const [setupState, setSetupState] = useState<SetupState>({
    providerId: initialProviderId,
    step: 'provider',
  })
  const [setupSubmitting, setSetupSubmitting] = useState(false)
  const [providerChoiceIndex, setProviderChoiceIndex] = useState(
    providerOptionIndex(initialProviderId),
  )
  const [slashSuggestionIndex, setSlashSuggestionIndex] = useState(0)
  const [cursorVisible, setCursorVisible] = useState(true)

  const messageId = useRef(0)

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
  }, [props.initialEnv, props.initialError])

  useEffect(() => {
    const intervalId = setInterval(() => {
      setCursorVisible(current => !current)
    }, 500)

    return () => {
      clearInterval(intervalId)
    }
  }, [])

  const slashCommandActive = mode === 'chat' && input.startsWith('/')

  const slashQuery = useMemo(() => {
    if (!slashCommandActive) return ''
    const [commandPart] = input.slice(1).split(' ')
    return (commandPart ?? '').toLowerCase()
  }, [input, slashCommandActive])

  const slashSuggestions = useMemo<SlashSuggestion[]>(() => {
    if (!slashCommandActive) return []

    const search = slashQuery.toLowerCase()
    return slashCommandList
      .filter(item => {
        if (!search) return true
        return (
          item.command.toLowerCase().includes(search) ||
          item.description.toLowerCase().includes(search) ||
          (item.hint?.toLowerCase() ?? '').includes(search)
        )
      })
      .map<SlashSuggestion>(item => ({
        command: item.command,
        description: item.description,
        hint: item.hint,
        value: `/${item.command}`,
      }))
  }, [input, slashCommandActive, slashQuery])

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

  useInput((receivedInput, key) => {
    const selectingProvider = mode === 'setup' && setupState.step === 'provider'
    const hasSlashSuggestions = slashCommandActive && slashSuggestions.length > 0 && !streaming

    if (key.ctrl && receivedInput === 'c') {
      exit()
      return
    }

    if (setupSubmitting) return

    if (selectingProvider) {
      if (key.upArrow) {
        setProviderChoiceIndex(current => cycleProviderChoice(current, -1))
        return
      }

      if (key.downArrow) {
        setProviderChoiceIndex(current => cycleProviderChoice(current, 1))
        return
      }

      if (key.return) {
        handleSubmit()
        return
      }

      // Ignore typing while provider selection is active; Enter will use the highlighted option.
      return
    }

    if (key.tab) {
      if (hasSlashSuggestions) {
        applySlashSuggestion(slashSuggestionIndex)
      }

      return
    }

    if (hasSlashSuggestions) {
      if (key.downArrow) {
        setSlashSuggestionIndex(current =>
          cycleIndex(current, 1, slashSuggestions.length),
        )
        return
      }

      if (key.upArrow) {
        setSlashSuggestionIndex(current =>
          cycleIndex(current, -1, slashSuggestions.length),
        )
        return
      }
    }

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

  function handleSubmit(): void {
    const value = input
    const trimmed = value.trim()
    setInput('')

    if (mode === 'chat' && slashCommandActive && slashSuggestions.length > 0 && !streaming) {
      const submission = buildSlashSubmissionValue(slashSuggestionIndex, value)
      if (submission.trim()) {
        handleChatInput(submission)
      }

      return
    }

    if (mode === 'setup') {
      const setupValue =
        setupState.step === 'provider' ? selectedProviderId(providerChoiceIndex) : trimmed

      handleSetupInput(setupValue)
      return
    }

    if (!trimmed) return

    handleChatInput(value)
  }

  function applySlashSuggestion(targetIndex: number): void {
    const nextValue = buildSlashSubmissionValue(targetIndex, input)
    if (!nextValue) return
    const needsSpace = nextValue.endsWith(' ') ? '' : ' '
    setInput(`${nextValue}${needsSpace}`)
  }

  function buildSlashSubmissionValue(targetIndex: number, baseInput: string): string {
    const suggestion = slashSuggestions[clampIndex(targetIndex, slashSuggestions.length)]
    if (!suggestion) return baseInput.trim()

    const [, ...args] = baseInput.slice(1).split(' ')
    const argsText = args.join(' ').trim()
    const argSegment = argsText ? ` ${argsText}` : ''
    return `/${suggestion.command}${argSegment}`.trim()
  }

  async function handleSetupInput(value: string): Promise<void> {
    if (setupSubmitting) {
      addSystemMessage('Setup in progress. Please wait.')
      return
    }

    const trimmed = value.trim()

    switch (setupState.step) {
      case 'apiKey': {
        if (!trimmed) {
          addSystemMessage('API key is required.')
          return
        }

        setSetupState(current => ({
          ...current,
          apiKey: trimmed,
          step: 'baseURL',
        }))
        setMaskInput(false)
        addSystemMessage(
          'API key captured. Optional: enter base URL override, or press Enter to skip.',
        )
        return
      }

      case 'baseURL': {
        setSetupState(current => ({
          ...current,
          baseURL: trimmed || undefined,
          step: 'model',
        }))
        addSystemMessage('Optional: enter model override, or press Enter to skip.')
        return
      }

      case 'instructions': {
        if (!setupState.apiKey) {
          handleMissingApiKey()
          return
        }

        const instructions = trimmed || undefined
        setSetupState(current => ({...current, instructions}))
        await persistSetupEnv({instructions})
        return
      }

      case 'model': {
        if (!setupState.apiKey) {
          handleMissingApiKey()
          return
        }

        const nextModel = trimmed || undefined

        if (setupState.providerId === 'openai-agents') {
          setSetupState(current => ({
            ...current,
            model: nextModel,
            step: 'instructions',
          }))
          addSystemMessage(
            'Optional: enter default agent instructions, or press Enter to use the default.',
          )
          return
        }

        setSetupState(current => ({ ...current, model: nextModel }))
        await persistSetupEnv({model: nextModel})
        return
      }

      case 'provider': {
        const providerId = selectedProviderId(providerChoiceIndex)

        setProviderChoiceIndex(providerOptionIndex(providerId))
        setSetupState({
          apiKey: undefined,
          baseURL: undefined,
          instructions: undefined,
          model: undefined,
          providerId,
          step: 'apiKey',
        })
        setMaskInput(true)
        addSystemMessage(`Using provider ${providerId}. Enter API key:`)
        break
      }
    }
  }

  function handleMissingApiKey(): void {
    addSystemMessage('Missing API key; restart setup with /login.')
    resetSetup(setupState.providerId)
  }

  async function persistSetupEnv(overrides: {instructions?: string; model?: string}): Promise<void> {
    const {apiKey} = setupState
    if (!apiKey) {
      handleMissingApiKey()
      return
    }

    try {
      persistProviderEnv({
        apiKey,
        baseURL: setupState.baseURL,
        instructions: overrides.instructions ?? setupState.instructions,
        model: overrides.model ?? setupState.model,
        providerId: setupState.providerId,
      })
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      addSystemMessage(`Failed to write .env: ${message}`)
      return
    }

    const { env, error } = tryLoadProviderEnv({ providerId: setupState.providerId })
    if (!env || error) {
      addSystemMessage(
        `Failed to load provider config. ${error ? error.message : 'Unknown error.'}`,
      )
      resetSetup(setupState.providerId)
      return
    }

    await finalizeSetup(env)
  }

  function resetSetup(nextProviderId: ProviderId = 'openai'): void {
    setMode('setup')
    setMaskInput(false)
    setProviderChoiceIndex(providerOptionIndex(nextProviderId))
    setSetupState({
      apiKey: undefined,
      baseURL: undefined,
      instructions: undefined,
      model: undefined,
      providerId: nextProviderId,
      step: 'provider',
    })
  }

  async function finalizeSetup(env: ProviderEnv): Promise<void> {
    setSetupSubmitting(true)
    addSystemMessage('Checking provider connectivity...')

    try {
      await pingProvider(env)
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      addSystemMessage(
        `Connectivity check failed for ${env.providerId}: ${message}. Please verify your API key or base URL.`,
      )
      setMode('setup')
      setMaskInput(true)
      setProviderChoiceIndex(providerOptionIndex(env.providerId))
      setSetupState({
        apiKey: undefined,
        baseURL: undefined,
        instructions: undefined,
        model: undefined,
        providerId: env.providerId,
        step: 'apiKey',
      })
      return
    } finally {
      setSetupSubmitting(false)
    }

    setProviderEnv(env)
    setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })
    setMode('chat')
    setProviderChoiceIndex(providerOptionIndex(env.providerId))
    setSetupState({
      apiKey: undefined,
      baseURL: undefined,
      instructions: undefined,
      model: undefined,
      providerId: env.providerId,
      step: 'provider',
    })
    addSystemMessage(
      `Configured ${env.providerId} (${env.model ?? 'default model'}). Type /help to see commands.`,
    )
  }

  function handleChatInput(value: string): void {
    if (value.startsWith('/')) {
      handleSlashCommand(value)
      return
    }

    if (!providerEnv) {
      addSystemMessage('Provider not configured. Run /login to set up credentials.')
      setMode('setup')
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

  const slashHandlers: Record<SlashCommandId, SlashHandler> = {
    clear: handleClearCommand,
    help: handleHelpCommand,
    login: handleLoginCommand,
    model: handleModelCommand,
    provider: handleProviderCommand,
  }

  function handleSlashCommand(raw: string): void {
    const [command, ...args] = raw
      .slice(1)
      .split(' ')
      .map(part => part.trim())
      .filter(Boolean)

    if (!command) {
      addSystemMessage('Empty command. Use /help to see available commands.')
      return
    }

    if (!isSlashCommand(command)) {
      addSystemMessage(`Unknown command: /${command}`)
      return
    }

    const handler = slashHandlers[command]
    handler(args)
  }

  function isSlashCommand(value: string): value is SlashCommandId {
    return slashCommandList.some(command => command.command === value)
  }

  function handleClearCommand(): void {
    setMessages([])
    setSessionMeta(undefined)
    setSessionStatus(undefined)
    setSessionStatusMessage(undefined)
    setSessionUsage(undefined)
    setCurrentResponse('')
    addSystemMessage('Cleared transcript.')
  }

  function handleHelpCommand(): void {
    const commands = slashCommandList.map(option => `/${option.command}`).join(', ')
    addSystemMessage(`Commands: ${commands}. Type / and use Tab to autocomplete.`)
  }

  function handleLoginCommand(): void {
    const nextProviderId = providerEnv?.providerId ?? setupState.providerId
    resetSetup(nextProviderId)
    addSystemMessage(
      'Restarting setup. Use arrow keys to choose provider (openai/openai-agents/anthropic/deepseek).',
    )
  }

  function handleModelCommand(args: string[]): void {
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

    setProviderEnv(current =>
      current
        ? {
            ...current,
            model,
          }
        : current,
    )
    setSessionMeta(current =>
      current
        ? {
            ...current,
            model,
          }
        : current,
    )
    addSystemMessage(`Model set to ${model}.`)
  }

  function handleProviderCommand(args: string[]): void {
    const providerId = parseProviderId(args[0] ?? '')
    if (!providerId) {
      addSystemMessage('Usage: /provider <openai|openai-agents|anthropic|deepseek>')
      return
    }

    const { env, error } = tryLoadProviderEnv({providerId})
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
    setSessionMeta({model: env.model ?? 'default', providerId: env.providerId})
    addSystemMessage(`Switched to ${providerId} (${env.model ?? 'default model'}).`)
  }

  function startStream(history: Message[], env: ProviderEnv): void {
    const prompt = buildPrompt(history)
    const controller = new ChatController({ env })
    let stream: SessionStream

    try {
      stream = controller.createStream({
        model: env.model,
        prompt,
        providerId: env.providerId,
      })
    } catch (error) {
      const message = error instanceof Error ? error.message : String(error)
      addSystemMessage(`Failed to start chat: ${message}`)
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
      addSystemMessage(`Provider error: ${message}`)
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
          const assistantMessage = createMessage('assistant', buffer)
          setMessages(current => [...current, assistantMessage])
        }
      } catch (error) {
        handleStreamError(error)
      } finally {
        setStreaming(false)
        setCurrentResponse('')
      }
    }

    streamChunks()
  }

  function addSystemMessage(text: string): void {
    setMessages(current => [...current, createMessage('system', text)])
  }

  function createMessage(role: MessageRole, text: string): Message {
    const id = messageId.current++
    return { id, role, text }
  }

  const inputLabel = '>'
  const renderedInput = maskInput ? '*'.repeat(input.length) : input
  const providerMeta = sessionMeta ?? (providerEnv ? createMetaFromEnv(providerEnv) : undefined)
  const providerSelection = selectedProviderId(providerChoiceIndex)
  const hint =
    mode === 'setup'
      ? setupPrompt(setupState.step, providerSelection)
      : 'Type a prompt or use /help, /login, /provider, /model, /clear'
  const placeholder = streaming
    ? 'Processing response...'
    : setupSubmitting
      ? 'Validating provider...'
      : hint
  const showCursor = !streaming && !setupSubmitting && cursorVisible
  const showProviderSelect = mode === 'setup' && setupState.step === 'provider'
  const showSlashSuggestions =
    !streaming && mode === 'chat' && slashCommandActive && slashSuggestions.length > 0

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
            {`Assistant: ${currentResponse || '...'}${sessionStatus === 'completed' || !cursorVisible ? '  ' : ' ▌'
              }`}
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

function providerOptionIndex(providerId: ProviderId): number {
  const index = providerOptions.findIndex(option => option.value === providerId)
  return index === -1 ? 0 : index
}

function selectedProviderId(index: number): ProviderId {
  return providerOptions[index]?.value ?? 'openai'
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

function buildPrompt(history: Message[]): string {
  const exchanges = history
    .filter(message => message.role !== 'system')
    .map(message => `${message.role === 'user' ? 'User' : 'Assistant'}: ${message.text}`)

  exchanges.push('Assistant:')

  return exchanges.join('\n')
}

function createMetaFromEnv(env: ProviderEnv): { model: string; providerId: ProviderId } {
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
