import {Box, Text, useApp, useInput} from 'ink'
import {useEffect, useRef, useState} from 'react'

import type {ProviderId, SessionStream, StreamStatus, TokenUsage} from '../core/stream.js'

import {ChatController} from '../chat/controller.js'
import {
  persistProviderEnv,
  type ProviderEnv,
  tryLoadProviderEnv,
} from '../config/env.js'
import {StatusBar} from './status-bar.js'

type Mode = 'chat' | 'setup'

type SetupStep = 'apiKey' | 'model' | 'provider'

type MessageRole = 'assistant' | 'system' | 'user'

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

  const [mode, setMode] = useState<Mode>(props.initialEnv ? 'chat' : 'setup')
  const [input, setInput] = useState('')
  const [maskInput, setMaskInput] = useState(false)
  const [messages, setMessages] = useState<Message[]>([])
  const [providerEnv, setProviderEnv] = useState<ProviderEnv | undefined>(props.initialEnv)
  const [sessionMeta, setSessionMeta] = useState<{model: string; providerId: ProviderId}>()
  const [sessionStatus, setSessionStatus] = useState<StreamStatus | undefined>()
  const [sessionUsage, setSessionUsage] = useState<TokenUsage | undefined>()
  const [streaming, setStreaming] = useState(false)
  const [currentResponse, setCurrentResponse] = useState('')
  const [setupState, setSetupState] = useState<SetupState>({
    providerId: props.initialEnv?.providerId ?? 'openai',
    step: 'provider',
  })

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
      addSystemMessage('No provider configured. Starting setup...')
    }
  }, [props.initialEnv, props.initialError])

  useInput((receivedInput, key) => {
    if (key.ctrl && receivedInput === 'c') {
      exit()
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
      handleSetupInput(trimmed)
      return
    }

    if (!trimmed) return

    handleChatInput(value)
  }

  function handleSetupInput(value: string): void {
    if (setupState.step === 'provider') {
      const providerId = parseProviderId(value) ?? (value ? undefined : 'openai')
      if (!providerId) {
        addSystemMessage('Provider must be one of: openai, anthropic, deepseek')
        return
      }

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

    const {env, error} = tryLoadProviderEnv({providerId: setupState.providerId})
    if (!env || error) {
      addSystemMessage(
        `Failed to load provider config. ${error ? error.message : 'Unknown error.'}`,
      )
      setMode('setup')
      setSetupState({
        providerId: 'openai',
        step: 'provider',
      })
      return
    }

    setProviderEnv(env)
    setSessionMeta({model: env.model ?? 'default', providerId: env.providerId})
    setMode('chat')
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

  function handleSlashCommand(raw: string): void {
    const [command, ...rest] = raw
      .slice(1)
      .split(' ')
      .map(part => part.trim())
      .filter(Boolean)

    if (!command) {
      addSystemMessage('Empty command. Use /help to see available commands.')
      return
    }

    switch (command) {
      case 'clear': {
        setMessages([])
        setSessionMeta(undefined)
        setSessionStatus(undefined)
        setSessionUsage(undefined)
        setCurrentResponse('')
        addSystemMessage('Cleared transcript.')
        break
      }

      case 'help': {
        addSystemMessage('Commands: /help, /login, /provider <id>, /model <name>, /clear')
        break
      }

      case 'login': {
        setMode('setup')
        setSetupState(current => ({
          ...current,
          apiKey: undefined,
          model: undefined,
          providerId: providerEnv?.providerId ?? current.providerId,
          step: 'provider',
        }))
        setMaskInput(false)
        addSystemMessage('Restarting setup. Choose provider (openai/anthropic/deepseek).')
        break
      }

      case 'model': {
        if (!providerEnv) {
          addSystemMessage('Provider not configured. Run /login first.')
          return
        }

        const model = rest.join(' ').trim()
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
          const message =
            persistError instanceof Error ? persistError.message : String(persistError)
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
        break
      }

      case 'provider': {
        const providerId = parseProviderId(rest[0] ?? '')
        if (!providerId) {
          addSystemMessage('Usage: /provider <openai|anthropic|deepseek>')
          return
        }

        const {env, error} = tryLoadProviderEnv({providerId})
        if (!env || error) {
          addSystemMessage(
            `Provider ${providerId} is not configured. Run /login to set API key first.`,
          )
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
          const message =
            persistError instanceof Error ? persistError.message : String(persistError)
          addSystemMessage(`Failed to switch provider: ${message}`)
          return
        }

        setProviderEnv(env)
        setSessionMeta({model: env.model ?? 'default', providerId: env.providerId})
        addSystemMessage(`Switched to ${providerId} (${env.model ?? 'default model'}).`)
        break
      }

      default: {
        addSystemMessage(`Unknown command: /${command}`)
      }
    }
  }

  function startStream(history: Message[], env: ProviderEnv): void {
    const prompt = buildPrompt(history)
    const controller = new ChatController({env})
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
    setSessionMeta({model: env.model ?? 'default', providerId: env.providerId})

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
              setSessionMeta({model: chunk.model, providerId: chunk.providerId})
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
    return {id, role, text}
  }

  const inputLabel = mode === 'setup' ? 'setup>' : 'aice>'
  const renderedInput = maskInput ? '*'.repeat(input.length) : input
  const providerMeta = sessionMeta ?? (providerEnv ? createMetaFromEnv(providerEnv) : undefined)
  const hint =
    mode === 'setup'
      ? setupPrompt(setupState.step)
      : 'Type a prompt or use /help, /login, /provider, /model, /clear'

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
            {`Assistant: ${currentResponse || '...'}${sessionStatus === 'completed' ? '' : ' ▌'}`}
          </Text>
        ) : null}
      </Box>
      <StatusBar meta={providerMeta} status={sessionStatus} usage={sessionUsage} />
      <Box>
        <Text color="yellow">{inputLabel}</Text>
        <Text>{` ${renderedInput}`}</Text>
      </Box>
      <Text dimColor>{hint}</Text>
    </Box>
  )
}

function parseProviderId(value: string): ProviderId | undefined {
  if (value === 'openai' || value === 'anthropic' || value === 'deepseek') {
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

function createMetaFromEnv(env: ProviderEnv): {model: string; providerId: ProviderId} {
  return {
    model: env.model ?? 'default',
    providerId: env.providerId,
  }
}

function setupPrompt(step: SetupStep): string {
  if (step === 'provider') {
    return 'Choose provider (openai/anthropic/deepseek). Press Enter for openai.'
  }

  if (step === 'apiKey') {
    return 'Enter API key (hidden as you type).'
  }

  if (step === 'model') {
    return 'Optional: enter model override, or press Enter to use the default.'
  }

  return ''
}
