import {Box, Text, useApp, useInput} from 'ink'
import {useEffect, useRef, useState} from 'react'

import type {ProviderId, SessionStream, StreamStatus, TokenUsage} from '../core/stream.js'

import {ChatController} from '../chat/controller.js'
import {persistProviderEnv, type ProviderEnv, tryLoadProviderEnv} from '../config/env.js'
import {InputPanel} from './input-panel.js'
import {SelectInput, type SelectInputItem} from './select-input.js'
import {StatusBar} from './status-bar.js'

type Mode = 'chat' | 'setup'

type SetupStep = 'apiKey' | 'model' | 'provider'

type MessageRole = 'assistant' | 'system' | 'user'

type SlashHandler = (args: string[]) => void

type ProviderOption = SelectInputItem<ProviderId>

const providerOptions: ProviderOption[] = [
  {description: 'Responses API (default)', label: 'OpenAI', value: 'openai'},
  {description: 'Agents API', label: 'OpenAI Agents', value: 'openai-agents'},
  {description: 'Claude 3.7 and newer', label: 'Anthropic', value: 'anthropic'},
  {description: 'DeepSeek chat + reasoning', label: 'DeepSeek', value: 'deepseek'},
]

interface Message {
  id: number
  role: MessageRole
  text: string
}

export interface AiceAppProps {
  initialEnv?: ProviderEnv
  initialError?: Error
}

interface SetupState {
  apiKey?: string
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
  const [sessionUsage, setSessionUsage] = useState<TokenUsage | undefined>()
  const [streaming, setStreaming] = useState(false)
  const [currentResponse, setCurrentResponse] = useState('')
  const [setupState, setSetupState] = useState<SetupState>({
    providerId: initialProviderId,
    step: 'provider',
  })
  const [providerChoiceIndex, setProviderChoiceIndex] = useState(
    providerOptionIndex(initialProviderId),
  )
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

  useInput((receivedInput, key) => {
    const selectingProvider = mode === 'setup' && setupState.step === 'provider'

    if (key.ctrl && receivedInput === 'c') {
      exit()
      return
    }

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

    if (mode === 'setup') {
      const setupValue =
        setupState.step === 'provider' ? selectedProviderId(providerChoiceIndex) : trimmed

      handleSetupInput(setupValue)
      return
    }

    if (!trimmed) return

    handleChatInput(value)
  }

  function handleSetupInput(value: string): void {
    if (setupState.step === 'provider') {
      const providerId = selectedProviderId(providerChoiceIndex)

      setProviderChoiceIndex(providerOptionIndex(providerId))
      setSetupState(current => ({
        ...current,
        providerId,
        step: 'apiKey',
      }))
      setMaskInput(true)
      addSystemMessage(`Using provider ${providerId}. Enter API key:`)
      return
    }

    if (setupState.step === 'apiKey') {
      if (!value) {
        addSystemMessage('API key is required.')
        return
      }

      setSetupState(current => ({
        ...current,
        apiKey: value,
        step: 'model',
      }))
      setMaskInput(false)
      addSystemMessage('API key captured. Optional: enter model override, or press Enter to skip.')
      return
    }

    if (!setupState.apiKey) {
      addSystemMessage('Missing API key; restart setup with /login.')
      setMode('setup')
      setMaskInput(false)
      setProviderChoiceIndex(providerOptionIndex('openai'))
      setSetupState({
        providerId: 'openai',
        step: 'provider',
      })
      return
    }

    const model = value || undefined

    try {
      persistProviderEnv({
        apiKey: setupState.apiKey,
        model,
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
      setMode('setup')
      setMaskInput(false)
      setProviderChoiceIndex(providerOptionIndex('openai'))
      setSetupState({
        providerId: 'openai',
        step: 'provider',
      })
      return
    }

    setProviderEnv(env)
    setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })
    setMode('chat')
    setProviderChoiceIndex(providerOptionIndex(env.providerId))
    setSetupState({
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

  const slashHandlers: Record<string, SlashHandler> = {
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

    const handler = slashHandlers[command]
    if (!handler) {
      addSystemMessage(`Unknown command: /${command}`)
      return
    }

    handler(args)
  }

  function handleClearCommand(): void {
    setMessages([])
    setSessionMeta(undefined)
    setSessionStatus(undefined)
    setSessionUsage(undefined)
    setCurrentResponse('')
    addSystemMessage('Cleared transcript.')
  }

  function handleHelpCommand(): void {
    addSystemMessage('Commands: /help, /login, /provider <id>, /model <name>, /clear')
  }

  function handleLoginCommand(): void {
    setMode('setup')
    setMaskInput(false)
    const nextProviderId = providerEnv?.providerId ?? setupState.providerId
    setProviderChoiceIndex(providerOptionIndex(nextProviderId))
    setSetupState(current => ({
      ...current,
      apiKey: undefined,
      model: undefined,
      providerId: nextProviderId,
      step: 'provider',
    }))
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

    const {env, error} = tryLoadProviderEnv({providerId})
    if (!env || error) {
      addSystemMessage(`Provider ${providerId} is not configured. Run /login to set API key first.`)
      return
    }

    try {
      persistProviderEnv({
        apiKey: env.apiKey,
        baseURL: env.baseURL,
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
    setSessionUsage(undefined)
    setSessionMeta({ model: env.model ?? 'default', providerId: env.providerId })

    let buffer = ''

    async function streamChunks() {
      try {
        for await (const chunk of stream) {
          switch (chunk.type) {
            case 'done': {
              setSessionStatus('completed')
              break
            }

            case 'error': {
              throw chunk.error
            }

            case 'meta': {
              setSessionMeta({ model: chunk.model, providerId: chunk.providerId })
              break
            }

            case 'status': {
              setSessionStatus(chunk.status)
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
        const message = error instanceof Error ? error.message : String(error)
        addSystemMessage(`Provider error: ${message}`)
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
  const placeholder = streaming ? 'Processing response...' : hint
  const showCursor = !streaming && cursorVisible
  const showProviderSelect = mode === 'setup' && setupState.step === 'provider'

  return (
    <Box flexDirection="column">
      <Text color="cyan">AICE</Text>
      <Box flexDirection="column" marginBottom={1}>
        {messages.map(message => (
          <Text color={colorForRole(message.role)} key={message.id} wrap="wrap">
            {`${labelForRole(message.role)} ${message.text}`}
          </Text>
        ))}
        {streaming ? (
          <Text color="green" wrap="wrap">
            {`Assistant: ${currentResponse || '...'}${sessionStatus === 'completed' || !cursorVisible ? '  ' : ' ▌'
              }`}
          </Text>
        ) : null}
      </Box>
      <Box width="100%">
        {showProviderSelect ? (
          <SelectInput
            active
            items={providerOptions}
            selectedIndex={providerChoiceIndex}
            title="Choose a provider"
          />
        ) : (
          <InputPanel
            cursorVisible={showCursor}
            disabled={streaming}
            label={inputLabel}
            placeholder={placeholder}
            value={renderedInput}
          />
        )}
      </Box>
      <Box width="100%">
        <StatusBar meta={providerMeta} status={sessionStatus} usage={sessionUsage} />
      </Box>
    </Box>
  )
}

function cycleProviderChoice(current: number, delta: number): number {
  const total = providerOptions.length
  if (total === 0) return 0
  return (current + delta + total) % total
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
  if (role === 'assistant') return 'green'
  if (role === 'user') return 'magenta'
  return 'gray'
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

  if (step === 'model') {
    return 'Optional: enter model override, or press Enter to use the default.'
  }

  return ''
}
