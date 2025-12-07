import type { OpenAI as OpenAIClient } from 'openai'
import type { ChatCompletionChunk } from 'openai/resources/chat/completions'

import { expect } from 'chai'

import type { ProviderStreamChunk } from '../../src/core/stream.ts'

import { DeepSeekProvider, type DeepSeekSessionRequest } from '../../src/providers/deepseek.ts'

type StreamChunk = ChatCompletionChunk | Error

class FakeCompletionStream implements AsyncIterable<StreamChunk> {
  aborted = false
  controller = {
    abort: () => {
      this.aborted = true
    },
  }
  private readonly events: StreamChunk[]

  constructor(events: StreamChunk[]) {
    this.events = events
  }

  async *[Symbol.asyncIterator]() {
    for (const event of this.events) {
      if (event instanceof Error) {
        throw event
      }

      yield event
    }
  }
}

class FakeCompletions {
  lastArgs?: {
    messages: unknown
    model: string
    signal?: AbortSignal
    temperature?: number
  }
  lastStream?: FakeCompletionStream
  private readonly events: StreamChunk[]

  constructor(events: StreamChunk[]) {
    this.events = events
  }

  async create(args: {
    messages: unknown
    model: string
    signal?: AbortSignal
    temperature?: number
  }): Promise<FakeCompletionStream> {
    this.lastArgs = args
    this.lastStream = new FakeCompletionStream(this.events)
    return this.lastStream
  }
}

class FakeOpenAI {
  chat: { completions: FakeCompletions }

  constructor(events: StreamChunk[]) {
    this.chat = { completions: new FakeCompletions(events) }
  }
}

describe('DeepSeekProvider', () => {
  it('streams delta, usage, and completion events in order', async () => {
    /* eslint-disable camelcase */
    const events: StreamChunk[] = [
      {
        choices: [{ delta: { role: 'assistant' }, finish_reason: null, index: 0 }],
        created: 0,
        id: 'chatcmpl-1',
        model: 'deepseek-chat',
        object: 'chat.completion.chunk',
      },
      {
        choices: [{ delta: { content: 'Hello' }, finish_reason: null, index: 0 }],
        created: 0,
        id: 'chatcmpl-1',
        model: 'deepseek-chat',
        object: 'chat.completion.chunk',
      },
      {
        choices: [{ delta: { content: ' world' }, finish_reason: null, index: 0 }],
        created: 0,
        id: 'chatcmpl-1',
        model: 'deepseek-chat',
        object: 'chat.completion.chunk',
      },
      {
        choices: [{ delta: {}, finish_reason: 'stop', index: 0 }],
        created: 0,
        id: 'chatcmpl-1',
        model: 'deepseek-chat',
        object: 'chat.completion.chunk',
        usage: { completion_tokens: 3, prompt_tokens: 2, total_tokens: 5 },
      },
    ]
    /* eslint-enable camelcase */

    const fakeClient = new FakeOpenAI(events)
    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      fakeClient as unknown as OpenAIClient,
    )

    const request: DeepSeekSessionRequest = {
      model: 'deepseek-chat',
      prompt: 'Hello?',
      providerId: 'deepseek',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    expect(chunks.map(chunk => chunk.type)).to.deep.equal(['status', 'text', 'text', 'usage', 'status'])
    expect(chunks.find(chunk => chunk.type === 'usage')).to.deep.include({
      usage: { inputTokens: 2, outputTokens: 3, totalTokens: 5 },
    })
    expect(fakeClient.chat.completions.lastArgs).to.deep.include({ model: 'deepseek-chat' })
    expect(fakeClient.chat.completions.lastStream?.aborted).to.equal(true)
  })

  it('emits failed status and error chunks when the stream reports a failure', async () => {
    /* eslint-disable camelcase */
    const events: StreamChunk[] = [
      {
        choices: [{ delta: { content: 'partial' }, finish_reason: null, index: 0 }],
        created: 0,
        id: 'chatcmpl-err',
        model: 'deepseek-chat',
        object: 'chat.completion.chunk',
      },
      new Error('DeepSeek response failed'),
    ]
    /* eslint-enable camelcase */

    const fakeClient = new FakeOpenAI(events)
    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      fakeClient as unknown as OpenAIClient,
    )

    const request: DeepSeekSessionRequest = {
      model: 'deepseek-chat',
      prompt: 'trigger',
      providerId: 'deepseek',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
      if (chunk.type === 'error') break
    }

    expect(chunks.some(chunk => chunk.type === 'error')).to.equal(true)
    expect(chunks.find(chunk => chunk.type === 'status' && chunk.status === 'failed')).to.exist
    const last = chunks.at(-1)
    if (last?.type === 'error') {
      expect(last.error.message).to.contain('DeepSeek response failed')
    }
  })
})
