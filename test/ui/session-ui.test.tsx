import { expect } from 'chai'
import { Box, Text, useApp } from 'ink'
import { render } from 'ink-testing-library'
import { type ReactElement, useCallback, useEffect, useRef, useState } from 'react'

import type { ProviderEnv } from '../../src/types/env.js'
import type { ProviderRequestInput } from '../../src/types/provider.js'
import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { useChatInputController } from '../../src/ui/hooks/use-chat-input-controller.js'
import { useChatStream } from '../../src/ui/hooks/use-chat-stream.js'
import { useSession } from '../../src/ui/hooks/use-session.js'
import { StatusBar } from '../../src/ui/status-bar.js'
import { createUsage } from '../helpers/usage.ts'

function stripAnsi(value: string): string {
  const escape = '\u001B['
  let cursor = 0
  let output = ''

  while (cursor < value.length) {
    const next = value.indexOf(escape, cursor)
    if (next === -1) {
      output += value.slice(cursor)
      break
    }

    output += value.slice(cursor, next)
    const end = value.indexOf('m', next)
    if (end === -1) break

    cursor = end + 1
  }

  return output
}

function countOccurrences(value: string, needle: string): number {
  if (!needle) return 0

  let count = 0
  let cursor = 0

  while (true) {
    const next = value.indexOf(needle, cursor)
    if (next === -1) break
    count += 1
    cursor = next + needle.length
  }

  return count
}

async function* chunkStream(chunks: ProviderStreamChunk[], delayMs = 0) {
  for (const chunk of chunks) {
    if (delayMs > 0) {
      // eslint-disable-next-line no-await-in-loop
      await delay(delayMs)
    }

    yield chunk
  }
}

async function* throwingStream(): AsyncGenerator<ProviderStreamChunk, void, void> {
  yield { id: 'text-0', text: 'partial', type: 'text-delta' }
  throw new Error('boom')
}

function delay(duration: number): Promise<void> {
  return new Promise(resolve => {
    setTimeout(resolve, duration)
  })
}

async function typeText(stdin: {write: (data: string) => void}, text: string): Promise<void> {
  for (const char of text) {
    stdin.write(char)
    // eslint-disable-next-line no-await-in-loop
    await delay(1)
  }
}

async function waitFor(condition: () => boolean, timeoutMs = 1000, intervalMs = 10): Promise<void> {
  const start = Date.now()

  while (Date.now() - start < timeoutMs) {
    if (condition()) return
    // eslint-disable-next-line no-await-in-loop
    await delay(intervalMs)
  }

  throw new Error('Timed out waiting for condition')
}

interface SessionSceneProps {
  stream: AsyncGenerator<ProviderStreamChunk, void, void>
}

function SessionScene(props: SessionSceneProps): ReactElement {
  const session = useSession({ stream: props.stream })
  const { exit } = useApp()

  useEffect(() => {
    if (session.done || session.error) {
      exit()
    }
  }, [exit, session.done, session.error])

  const content = session.content.trim() ? session.content : 'Waiting for the model...'

  return (
    <Box flexDirection="column">
      <Text>{content}</Text>
      <StatusBar
        status={session.status}
        statusMessage={session.statusMessage}
        usage={session.usage}
      />
      {session.error ? <Text color="red">{`Error: ${session.error.message}`}</Text> : null}
    </Box>
  )
}

describe('Ink UI', () => {
  describe('useSession', () => {
    it('renders streaming content and status updates', async () => {
      const stream = chunkStream(
        [
          { type: 'start' },
          { id: 'text-0', text: 'Hello', type: 'text-delta' },
          { id: 'text-0', text: ' world', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 5, outputTokens: 7, totalTokens: 12 }),
            type: 'finish',
          },
        ],
        5,
      )

      const { frames, lastFrame } = render(<SessionScene stream={stream} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:completed'))

      const cleanedFrames = frames.map(frame => stripAnsi(frame))
      const partialFrame = cleanedFrames.find(
        frame => frame.includes('Hello') && !frame.includes('Hello world'),
      )

      expect(partialFrame).to.not.equal(undefined)

      const finalFrame = stripAnsi(lastFrame() ?? '')

      expect(finalFrame).to.include('Hello world')
      expect(finalFrame).to.include('provider:-')
      expect(finalFrame).to.include('status:completed')
      expect(finalFrame).to.include('usage in=5 out=7 total=12')
    })

    it('marks aborted streams and keeps partial output', async () => {
      const stream = chunkStream(
        [
          { type: 'start' },
          { id: 'text-0', text: 'Hello', type: 'text-delta' },
          { type: 'abort' },
        ],
        5,
      )

      const { lastFrame } = render(<SessionScene stream={stream} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:aborted'))

      const finalFrame = stripAnsi(lastFrame() ?? '').replaceAll(/\s+/g, ' ')

      expect(finalFrame).to.include('Hello')
      expect(finalFrame).to.include('status:aborted - Aborted')
    })

    it('uses finish-step usage when no final finish chunk arrives', async () => {
      const response = { id: 'resp-0', modelId: 'deepseek-chat', timestamp: new Date(0) }
      const stream = chunkStream(
        [
          { type: 'start' },
          {
            finishReason: 'stop',
            providerMetadata: undefined,
            rawFinishReason: 'stop',
            response,
            type: 'finish-step',
            usage: createUsage({ inputTokens: 1, outputTokens: 2, totalTokens: 3 }),
          },
        ],
        5,
      )

      const { lastFrame } = render(<SessionScene stream={stream} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:completed'))

      const finalFrame = stripAnsi(lastFrame() ?? '')

      expect(finalFrame).to.include('usage in=1 out=2 total=3')
      expect(finalFrame).to.include('status:completed')
    })

    it('appends reasoning deltas to the transcript', async () => {
      const stream = chunkStream(
        [
          { type: 'start' },
          { id: 'reason-0', text: 'thinking', type: 'reasoning-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
            type: 'finish',
          },
        ],
        5,
      )

      const { lastFrame } = render(<SessionScene stream={stream} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:completed'))

      const finalFrame = stripAnsi(lastFrame() ?? '')

      expect(finalFrame).to.include('thinking')
    })

    it('surfaces error chunks', async () => {
      const stream = chunkStream([
        { id: 'text-0', text: 'partial', type: 'text-delta' },
        { error: new Error('boom'), type: 'error' },
      ])

      const { lastFrame } = render(<SessionScene stream={stream} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:failed'))

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(finalFrame.replaceAll(/\s+/g, ' ')).to.include('status:failed - boom')
      expect(finalFrame).to.include('Error: boom')
    })

    it('surfaces thrown stream errors', async () => {
      const { lastFrame } = render(<SessionScene stream={throwingStream()} />)
      await waitFor(() => (lastFrame() ?? '').includes('status:failed'))

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(finalFrame.replaceAll(/\s+/g, ' ')).to.include('status:failed - boom')
      expect(finalFrame).to.include('Error: boom')
    })
  })

  interface ChatStreamSceneProps {
    env: ProviderEnv
    stream: AsyncGenerator<ProviderStreamChunk, void, void>
  }

  function ChatStreamScene(props: ChatStreamSceneProps): ReactElement {
    const startedRef = useRef(false)
    const { exit } = useApp()
    const [messages, setMessages] = useState<string[]>([])

    const handleAssistantMessage = useCallback((message: string) => {
      setMessages(current => [...current, `assistant:${message}`])
    }, [])

    const handleSystemMessage = useCallback((message: string) => {
      setMessages(current => [...current, `system:${message}`])
    }, [])

    const { currentResponse, sessionStatus, startStream, streaming } = useChatStream({
      buildMessages: () => [{ content: 'prompt', role: 'user' }],
      createChatService: () => ({
        createStream: (_env: ProviderEnv, _input: ProviderRequestInput) => props.stream,
      }),
      onAssistantMessage: handleAssistantMessage,
      onSystemMessage: handleSystemMessage,
    })

    useEffect(() => {
      if (startedRef.current) return
      startedRef.current = true
      startStream([{ id: 0, role: 'user', text: 'Hello?' }], props.env)
    }, [props.env, startStream])

    useEffect(() => {
      const hasTerminalMessage = messages.some(message =>
        message.startsWith('assistant:') || message.startsWith('system:Provider error:'),
      )

      if (hasTerminalMessage) {
        exit()
      }
    }, [exit, messages])

    return (
      <Box flexDirection="column">
        {messages.map((message, index) => (
          <Text key={index}>{message}</Text>
        ))}
        <Text>{streaming ? `stream:${currentResponse}` : 'idle'}</Text>
        <Text>{`status:${sessionStatus ?? 'pending'}`}</Text>
      </Box>
    )
  }

  interface FailingChatStreamSceneProps {
    env: ProviderEnv
  }

  interface ChatInputControllerSceneProps {
    env: ProviderEnv
    streams: Array<AsyncGenerator<ProviderStreamChunk, void, void>>
  }

  function ChatInputControllerScene(props: ChatInputControllerSceneProps): ReactElement {
    const { exit } = useApp()
    const streamsRef = useRef([...props.streams])
    const controller = useChatInputController({
      createChatService: () => ({
        createStream() {
          const nextStream = streamsRef.current.shift()
          if (!nextStream) {
            throw new Error('No stream available')
          }

          return nextStream
        },
      }),
      initialEnv: props.env,
    })

    useEffect(() => {
      const assistantCount = controller.messages.filter(message => message.role === 'assistant').length
      if (assistantCount >= 2 && !controller.streaming) {
        exit()
      }
    }, [controller.messages, controller.streaming, exit])

    return (
      <Box flexDirection="column">
        {controller.messages.map(message => (
          <Text key={message.id}>{`${message.role}:${message.text}`}</Text>
        ))}
        <Text>{controller.streaming ? `stream:${controller.currentResponse}` : 'idle'}</Text>
      </Box>
    )
  }

  function FailingChatStreamScene(props: FailingChatStreamSceneProps): ReactElement {
    const startedRef = useRef(false)
    const { exit } = useApp()
    const [messages, setMessages] = useState<string[]>([])

    const handleSystemMessage = useCallback((message: string) => {
      setMessages(current => [...current, `system:${message}`])
    }, [])

    const { startStream, streaming } = useChatStream({
      buildMessages: () => [{ content: 'prompt', role: 'user' }],
      createChatService: () => ({
        createStream() {
          throw new Error('no stream')
        },
      }),
      onSystemMessage: handleSystemMessage,
    })

    useEffect(() => {
      if (startedRef.current) return
      startedRef.current = true
      startStream([{ id: 0, role: 'user', text: 'Hello?' }], props.env)
    }, [props.env, startStream])

    useEffect(() => {
      if (messages.some(message => message.startsWith('system:Failed to start agent:'))) {
        exit()
      }
    }, [exit, messages])

    return (
      <Box flexDirection="column">
        {messages.map((message, index) => (
          <Text key={index}>{message}</Text>
        ))}
        <Text>{streaming ? 'streaming' : 'idle'}</Text>
      </Box>
    )
  }

  interface MultiChatStreamSceneProps {
    env: ProviderEnv
    streams: Array<AsyncGenerator<ProviderStreamChunk, void, void>>
  }

  function MultiChatStreamScene(props: MultiChatStreamSceneProps): ReactElement {
    const { exit } = useApp()
    const [messages, setMessages] = useState<string[]>([])
    const startedRef = useRef(0)
    const streamsRef = useRef([...props.streams])

    const handleAssistantMessage = useCallback((message: string) => {
      setMessages(current => [...current, `assistant:${message}`])
    }, [])

    const { sessionStatus, startStream, streaming } = useChatStream({
      buildMessages: () => [{ content: 'prompt', role: 'user' }],
      createChatService: () => ({
        createStream() {
          const nextStream = streamsRef.current.shift()
          if (!nextStream) {
            throw new Error('No stream available')
          }

          return nextStream
        },
      }),
      onAssistantMessage: handleAssistantMessage,
    })

    const startNext = useCallback(() => {
      const index = startedRef.current
      startStream([{ id: index, role: 'user', text: `message-${index}` }], props.env)
      startedRef.current += 1
    }, [props.env, startStream])

    useEffect(() => {
      if (startedRef.current === 0) {
        startNext()
      }
    }, [startNext])

    useEffect(() => {
      if (startedRef.current === 1 && sessionStatus === 'completed' && !streaming) {
        startNext()
      }
    }, [sessionStatus, startNext, streaming])

    useEffect(() => {
      if (messages.length >= 2) {
        exit()
      }
    }, [exit, messages.length])

    return (
      <Box flexDirection="column">
        {messages.map((message, index) => (
          <Text key={index}>{message}</Text>
        ))}
        <Text>{`status:${sessionStatus ?? 'pending'}`}</Text>
      </Box>
    )
  }

  describe('useChatStream', () => {
    it('streams text and commits the assistant message on completion', async () => {
      const stream = chunkStream(
        [
          { type: 'start' },
          { id: 'text-0', text: 'Hello', type: 'text-delta' },
          { id: 'text-0', text: ' world', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 2, outputTokens: 2, totalTokens: 4 }),
            type: 'finish',
          },
        ],
        5,
      )

      const env: ProviderEnv = {
        apiKey: 'test-key',
        model: 'deepseek-chat',
        providerId: 'deepseek',
      }

      const { frames, lastFrame } = render(<ChatStreamScene env={env} stream={stream} />)
      await waitFor(() => stripAnsi(lastFrame() ?? '').includes('assistant:Hello world'))

      const cleanedFrames = frames.map(frame => stripAnsi(frame))
      const partialFrame = cleanedFrames.find(
        frame => frame.includes('stream:Hello') && !frame.includes('Hello world'),
      )

      expect(partialFrame).to.not.equal(undefined)

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(finalFrame).to.include('assistant:Hello world')
      expect(finalFrame).to.include('idle')
      expect(finalFrame).to.include('status:completed')
    })

    it('commits distinct responses across sequential streams', async () => {
      const streams = [
        chunkStream([
          { type: 'start' },
          { id: 'text-0', text: 'First response', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
            type: 'finish',
          },
        ]),
        chunkStream([
          { type: 'start' },
          { id: 'text-0', text: 'Second response', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
            type: 'finish',
          },
        ]),
      ]

      const env: ProviderEnv = {
        apiKey: 'test-key',
        model: 'deepseek-chat',
        providerId: 'deepseek',
      }

      const { lastFrame } = render(<MultiChatStreamScene env={env} streams={streams} />)
      await waitFor(() => stripAnsi(lastFrame() ?? '').includes('assistant:Second response'))

      const finalFrame = stripAnsi(lastFrame() ?? '')

      expect(finalFrame).to.include('assistant:First response')
      expect(finalFrame).to.include('assistant:Second response')
    })

    it('reports failures when starting a chat stream', async () => {
      const env: ProviderEnv = {
        apiKey: 'test-key',
        model: 'deepseek-chat',
        providerId: 'deepseek',
      }

      const { lastFrame } = render(<FailingChatStreamScene env={env} />)
      await waitFor(() =>
        stripAnsi(lastFrame() ?? '').includes('system:Failed to start agent: no stream'),
      )

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(finalFrame).to.include('system:Failed to start agent: no stream')
      expect(finalFrame).to.include('idle')
    })

    it('surfaces provider errors as system messages and does not commit partial assistant output', async () => {
      const stream = chunkStream([
        { id: 'text-0', text: 'partial', type: 'text-delta' },
        { error: new Error('boom'), type: 'error' },
      ])

      const env: ProviderEnv = {
        apiKey: 'test-key',
        model: 'deepseek-chat',
        providerId: 'deepseek',
      }

      const { lastFrame } = render(<ChatStreamScene env={env} stream={stream} />)
      await waitFor(() => stripAnsi(lastFrame() ?? '').includes('system:Provider error: boom'))

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(finalFrame).to.include('system:Provider error: boom')
      expect(finalFrame).to.not.include('assistant:partial')
      expect(finalFrame).to.include('status:failed')
    })
  })

  describe('useChatInputController', () => {
    it('commits sequential responses without duplicating earlier output', async () => {
      const streams = [
        chunkStream([
          { type: 'start' },
          { id: 'text-0', text: 'First response', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
            type: 'finish',
          },
        ]),
        chunkStream([
          { type: 'start' },
          { id: 'text-0', text: 'Second response', type: 'text-delta' },
          {
            finishReason: 'stop',
            rawFinishReason: 'stop',
            totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
            type: 'finish',
          },
        ]),
      ]

      const env: ProviderEnv = {
        apiKey: 'test-key',
        model: 'deepseek-chat',
        providerId: 'deepseek',
      }

      const { lastFrame, stdin, unmount } = render(
        <ChatInputControllerScene env={env} streams={streams} />,
      )

      await typeText(stdin, 'Hello')
      stdin.write('\r')
      await waitFor(() => stripAnsi(lastFrame() ?? '').includes('assistant:First response'))

      await typeText(stdin, 'Hello again')
      stdin.write('\r')
      await waitFor(() => stripAnsi(lastFrame() ?? '').includes('assistant:Second response'))

      const finalFrame = stripAnsi(lastFrame() ?? '')
      expect(countOccurrences(finalFrame, 'assistant:First response')).to.equal(1)
      expect(countOccurrences(finalFrame, 'assistant:Second response')).to.equal(1)
      expect(finalFrame).to.include('idle')

      unmount()
    })
  })
})
