import {expect} from 'chai'

import type {ProviderStreamChunk} from '../../src/core/stream.ts'

import {AnthropicProvider, type AnthropicSessionRequest} from '../../src/providers/anthropic.ts'

type StreamEvent = {
  delta?: {text?: string; type?: string; usage?: Usage}
  error?: {message?: string}
  type: string
}

type Usage = {
  input_tokens?: number
  output_tokens?: number
  total_tokens?: number
}

class FakeStream implements AsyncIterable<StreamEvent> {
  finalUsage?: Usage
  private readonly events: StreamEvent[]

  constructor(events: StreamEvent[], finalUsage?: Usage) {
    this.events = events
    this.finalUsage = finalUsage
  }

  async finalMessage(): Promise<{usage?: Usage}> {
    return {usage: this.finalUsage}
  }

  async *[Symbol.asyncIterator]() {
    for (const event of this.events) {
      yield event
    }
  }
}

class FakeMessages {
  lastArgs?: Record<string, unknown>
  private readonly events: StreamEvent[]
  private readonly usage?: Usage

  constructor(events: StreamEvent[], usage?: Usage) {
    this.events = events
    this.usage = usage
  }

  async create(args: Record<string, unknown>): Promise<FakeStream> {
    this.lastArgs = args
    return new FakeStream(this.events, this.usage)
  }
}

class FakeAnthropic {
  messages: FakeMessages

  constructor(events: StreamEvent[], usage?: Usage) {
    this.messages = new FakeMessages(events, usage)
  }
}

type AnthropicLikeClient = {
  messages: {
    create: (args: Record<string, unknown>) => Promise<AsyncIterable<StreamEvent> & {finalMessage?: () => Promise<{usage?: Usage}>}>
  }
}

describe('AnthropicProvider', () => {
  it('streams deltas, final usage, and completion status', async () => {
    const events: StreamEvent[] = [
      {type: 'message_start'},
      {delta: {text: 'Hello', type: 'text_delta'}, type: 'content_block_delta'},
      {delta: {text: ' world', type: 'text_delta'}, type: 'content_block_delta'},
      {type: 'message_stop'},
    ]

    /* eslint-disable camelcase */
    const usage = {input_tokens: 3, output_tokens: 4, total_tokens: 7}
    /* eslint-enable camelcase */
    const client = new FakeAnthropic(events, usage)
    const provider = new AnthropicProvider(
      {apiKey: 'key', model: 'claude-3-5-sonnet-latest'},
      client as unknown as AnthropicLikeClient,
    )

    const request: AnthropicSessionRequest = {
      model: 'claude-3-5-sonnet-latest',
      prompt: 'hi',
      providerId: 'anthropic',
    }

    const chunks: ProviderStreamChunk[] = []
    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    expect(chunks.map(chunk => chunk.type)).to.deep.equal([
      'status',
      'text',
      'text',
      'usage',
      'status',
    ])
    expect(chunks[1]).to.include({text: 'Hello'})
    expect(chunks[3]).to.deep.include({usage: {inputTokens: 3, outputTokens: 4, totalTokens: 7}})
    expect(client.messages.lastArgs).to.have.property('stream', true)
  })

  it('emits failed status and error chunks when the stream reports an error', async () => {
    const events: StreamEvent[] = [
      {type: 'message_start'},
      {delta: {text: 'partial'}, type: 'content_block_delta'},
      {error: {message: 'boom'}, type: 'error'},
      {type: 'message_stop'},
    ]

    const client = new FakeAnthropic(events)
    const provider = new AnthropicProvider(
      {apiKey: 'key', model: 'claude-3-5-sonnet-latest'},
      client as unknown as AnthropicLikeClient,
    )

    const request: AnthropicSessionRequest = {
      model: 'claude-3-5-sonnet-latest',
      prompt: 'error',
      providerId: 'anthropic',
    }

    const chunks: ProviderStreamChunk[] = []
    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
      if (chunk.type === 'error') break
    }

    expect(chunks.find(chunk => chunk.type === 'status' && chunk.status === 'failed')).to.exist
    const last = chunks.at(-1)
    expect(last?.type).to.equal('error')
    expect(last?.error.message).to.contain('boom')
  })

  it('emits a single usage chunk when both delta and final usages are available', async () => {
    /* eslint-disable camelcase */
    const events: StreamEvent[] = [
      {type: 'message_start'},
      {delta: {usage: {input_tokens: 1, output_tokens: 1, total_tokens: 2}}, type: 'message_delta'},
      {type: 'message_stop'},
    ]

    const client = new FakeAnthropic(
      events,
      {input_tokens: 3, output_tokens: 4, total_tokens: 7},
    )
    /* eslint-enable camelcase */
    const provider = new AnthropicProvider(
      {apiKey: 'key', model: 'claude-3-5-sonnet-latest'},
      client as unknown as AnthropicLikeClient,
    )

    const request: AnthropicSessionRequest = {
      model: 'claude-3-5-sonnet-latest',
      prompt: 'usage',
      providerId: 'anthropic',
    }

    const chunks: ProviderStreamChunk[] = []
    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    const usageChunks = chunks.filter(chunk => chunk.type === 'usage')
    expect(usageChunks).to.have.length(1)
    expect(usageChunks[0]).to.deep.include({
      usage: {inputTokens: 3, outputTokens: 4, totalTokens: 7},
    })
  })
})
