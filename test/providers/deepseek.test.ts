import type {OpenAI as OpenAIClient} from 'openai'
import type {ResponseStreamEvent} from 'openai/resources/responses/responses'

import {expect} from 'chai'

import type {ProviderStreamChunk} from '../../src/core/stream.ts'

import {DeepSeekProvider, type DeepSeekSessionRequest} from '../../src/providers/deepseek.ts'

type StreamEvent = ResponseStreamEvent | {[key: string]: unknown; type: string}

class FakeResponseStream implements AsyncIterable<StreamEvent> {
  aborted = false
  controller = {
    abort: () => {
      this.aborted = true
    },
  }
  private readonly events: StreamEvent[]

  constructor(events: StreamEvent[]) {
    this.events = events
  }

  async *[Symbol.asyncIterator]() {
    for (const event of this.events) {
      yield event
    }
  }
}

class FakeResponses {
  lastArgs?: {
    input: unknown
    model: string
    signal?: AbortSignal
    temperature?: number
  }
  lastStream?: FakeResponseStream
  private readonly events: StreamEvent[]

  constructor(events: StreamEvent[]) {
    this.events = events
  }

  async stream(args: {
    input: unknown
    model: string
    signal?: AbortSignal
    temperature?: number
  }): Promise<FakeResponseStream> {
    this.lastArgs = args
    this.lastStream = new FakeResponseStream(this.events)
    return this.lastStream
  }
}

class FakeOpenAI {
  responses: FakeResponses

  constructor(events: StreamEvent[]) {
    this.responses = new FakeResponses(events)
  }
}

describe('DeepSeekProvider', () => {
  it('streams delta, usage, and completion events in order', async () => {
    const events: StreamEvent[] = [
      {type: 'response.in_progress'} as StreamEvent,
      {delta: 'Hello', type: 'response.output_text.delta'} as StreamEvent,
      {delta: ' world', type: 'response.output_text.delta'} as StreamEvent,
      {
        response: {
          /* eslint-disable camelcase */
          usage: {input_tokens: 2, output_tokens: 3, total_tokens: 5},
          /* eslint-enable camelcase */
        },
        type: 'response.completed',
      } as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new DeepSeekProvider(
      {apiKey: 'deepseek-key', model: 'deepseek-chat'},
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
      usage: {inputTokens: 2, outputTokens: 3, totalTokens: 5},
    })
    expect(fakeClient.responses.lastArgs).to.deep.include({model: 'deepseek-chat'})
    expect(fakeClient.responses.lastStream?.aborted).to.equal(true)
  })

  it('emits error chunks when the stream reports a failure', async () => {
    const events: StreamEvent[] = [
      {delta: 'partial', type: 'response.output_text.delta'} as StreamEvent,
      {type: 'response.failed'} as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new DeepSeekProvider(
      {apiKey: 'deepseek-key', model: 'deepseek-chat'},
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
  })
})
