import type { OpenAI as OpenAIClient } from 'openai'
import type { ResponseStreamEvent } from 'openai/resources/responses/responses'

import { expect } from 'chai'

import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { OpenAIProvider, type OpenAISessionRequest } from '../../src/providers/openai.ts'

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

describe('OpenAIProvider', () => {
  it('requires an API key', () => {
    expect(() => new OpenAIProvider({ apiKey: '' })).to.throw('Missing OpenAI API key')
  })

  it('streams delta, usage, and completion events in order', async () => {
    const events: StreamEvent[] = [
      { delta: 'Hello', type: 'response.output_text.delta' } as StreamEvent,
      { delta: ' world', type: 'response.output_text.delta' } as StreamEvent,
      {
        response: {
          /* eslint-disable camelcase */
          usage: { input_tokens: 5, output_tokens: 7, total_tokens: 12 },
          /* eslint-enable camelcase */
        },
        'sequence_number': 3,
        type: 'response.completed',
      } as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider(
      { apiKey: 'test-key', model: 'gpt-4o-mini' },
      fakeClient as unknown as OpenAIClient,
    )

    const request = {
      prompt: 'Hello?',
      providerId: 'openai',
      systemPrompt: 'You are helpful.',
      temperature: 0.1,
    } as OpenAISessionRequest

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    const types = chunks.map(chunk => chunk.type)
    expect(types).to.deep.equal(['status', 'text', 'text', 'usage', 'status'])
    expect(chunks[0]).to.include({ status: 'running' })
    expect(chunks[1]).to.include({ text: 'Hello' })
    expect(chunks[3]).to.deep.include({ usage: { inputTokens: 5, outputTokens: 7, totalTokens: 12 } })
    expect(chunks[4]).to.include({ status: 'completed' })

    expect(fakeClient.responses.lastArgs).to.deep.include({
      model: 'gpt-4o-mini',
      temperature: 0.1,
    })
    expect(fakeClient.responses.lastStream?.aborted).to.equal(true)
  })

  it('emits failed status and error chunks when the stream reports response.error', async () => {
    const events: StreamEvent[] = [
      { delta: 'partial', type: 'response.output_text.delta' } as StreamEvent,
      { code: 'rate_limit_exceeded', message: 'boom', type: 'error' } as StreamEvent,
      { type: 'response.completed' } as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider(
      { apiKey: 'test-key', model: 'gpt-4o-mini' },
      fakeClient as unknown as OpenAIClient,
    )

    const request: OpenAISessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'trigger',
      providerId: 'openai',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
      if (chunk.type === 'error') break
    }

    const statusChunk = chunks.find(chunk => chunk.type === 'status' && chunk.status === 'failed')
    expect(statusChunk).to.exist

    const lastChunk = chunks.at(-1)
    expect(lastChunk?.type).to.equal('error')
    expect(lastChunk).to.have.property('error')
    expect(lastChunk?.error.message).to.contain('rate_limit_exceeded')
  })

  it('falls back to a default message when response.error has no message', async () => {
    const events: StreamEvent[] = [
      {
        response: { error: {} },
        type: 'response.failed',
      } as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider(
      { apiKey: 'test-key', model: 'gpt-4o-mini' },
      fakeClient as unknown as OpenAIClient,
    )

    const request: OpenAISessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'trigger',
      providerId: 'openai',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
      if (chunk.type === 'error') break
    }

    const errorChunk = chunks.find(chunk => chunk.type === 'error')
    expect(errorChunk).to.exist
    expect(errorChunk?.error.message).to.equal('OpenAI response failed')
  })

  it('falls back to a default message when stream error has no message', async () => {
    const events: StreamEvent[] = [
      { code: 'rate_limit_exceeded', type: 'error' } as StreamEvent,
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider(
      { apiKey: 'test-key', model: 'gpt-4o-mini' },
      fakeClient as unknown as OpenAIClient,
    )

    const request: OpenAISessionRequest = {
      model: 'gpt-4o-mini',
      prompt: 'trigger',
      providerId: 'openai',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
      if (chunk.type === 'error') break
    }

    const errorChunk = chunks.find(chunk => chunk.type === 'error')
    expect(errorChunk).to.exist
    expect(errorChunk?.error.message).to.equal('rate_limit_exceeded: OpenAI stream error')
  })
})
