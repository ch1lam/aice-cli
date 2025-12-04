import {expect} from 'chai'
import {Box, Text, useApp} from 'ink'
import {render} from 'ink-testing-library'
import {type ReactElement, useEffect} from 'react'

import type {SessionStreamChunk} from '../../src/core/stream.ts'

import {ChatWindow} from '../../src/ui/chat-window.js'
import {useSession} from '../../src/ui/hooks/use-session.js'
import {StatusBar} from '../../src/ui/status-bar.js'

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

async function* chunkStream(chunks: SessionStreamChunk[], delayMs = 0) {
  for (const chunk of chunks) {
    if (delayMs > 0) {
      // eslint-disable-next-line no-await-in-loop
      await delay(delayMs)
    }

    yield chunk
  }
}

function delay(duration: number): Promise<void> {
  return new Promise(resolve => {
    setTimeout(resolve, duration)
  })
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
  stream: AsyncGenerator<SessionStreamChunk, void, void>
}

function SessionScene(props: SessionSceneProps): ReactElement {
  const session = useSession({stream: props.stream})
  const {exit} = useApp()

  useEffect(() => {
    if (session.done || session.error) {
      exit()
    }
  }, [exit, session.done, session.error])

  return (
    <Box flexDirection="column">
      <ChatWindow content={session.content} prompt="Hello?" />
      <StatusBar
        meta={session.meta}
        status={session.status}
        statusMessage={session.statusMessage}
        usage={session.usage}
      />
      {session.error ? <Text color="red">{`Error: ${session.error.message}`}</Text> : null}
    </Box>
  )
}

describe('Ink session UI', () => {
  it('renders streaming content and status updates', async () => {
    const stream = chunkStream(
      [
        {model: 'gpt-4o-mini', providerId: 'openai', type: 'meta'},
        {status: 'running', type: 'status'},
        {index: 0, text: 'Hello', type: 'text'},
        {index: 1, text: ' world', type: 'text'},
        {type: 'usage', usage: {inputTokens: 5, outputTokens: 7, totalTokens: 12}},
        {type: 'done'},
      ],
      5,
    )

    const {frames, lastFrame} = render(<SessionScene stream={stream} />)
    await waitFor(() => (lastFrame() ?? '').includes('status:completed'))

    const cleanedFrames = frames.map(frame => stripAnsi(frame))
    const partialFrame = cleanedFrames.find(
      frame => frame.includes('Hello') && !frame.includes('Hello world'),
    )

    expect(partialFrame).to.not.equal(undefined)

    const finalFrame = stripAnsi(lastFrame() ?? '')

    expect(finalFrame).to.include('Hello world')
    expect(finalFrame).to.include('openai:gpt-4o-mini')
    expect(finalFrame).to.include('status:completed')
    expect(finalFrame).to.include('usage in=5 out=7 total=12')
  })

  it('surfaces error chunks', async () => {
    const stream = chunkStream([
      {model: 'gpt-4o-mini', providerId: 'openai', type: 'meta'},
      {index: 0, text: 'partial', type: 'text'},
      {error: new Error('boom'), type: 'error'},
    ])

    const {lastFrame} = render(<SessionScene stream={stream} />)
    await waitFor(() => (lastFrame() ?? '').includes('status:failed'))

    const finalFrame = stripAnsi(lastFrame() ?? '')
    expect(finalFrame.replaceAll(/\s+/g, ' ')).to.include('status:failed - boom')
    expect(finalFrame).to.include('Error: boom')
  })
})
