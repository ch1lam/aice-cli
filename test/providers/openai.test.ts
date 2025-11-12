import type OpenAI from 'openai'

import {expect} from 'chai'

import type {ProviderStreamChunk} from '../../src/core/stream.ts'

import {OpenAIProvider, type OpenAISessionRequest} from '../../src/providers/openai.ts'

type StreamEvent = OpenAI.Client.Responses.Stream.Event

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
    expect(() => new OpenAIProvider({apiKey: ''})).to.throw('Missing OpenAI API key')
  })

  it('streams delta, usage, and completion events in order', async () => {
    const events: StreamEvent[] = [
      {delta: 'Hello', type: 'response.output_text.delta'},
      {delta: ' world', type: 'response.output_text.delta'},
      {
        type: 'response.output_usage',
        /* eslint-disable camelcase */
        usage: {input_tokens: 5, output_tokens: 7, total_tokens: 12},
        /* eslint-enable camelcase */
      },
      {type: 'response.completed'},
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider(
      {apiKey: 'test-key', model: 'gpt-4o-mini'},
      fakeClient as unknown as OpenAI,
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
    expect(types).to.deep.equal(['text', 'text', 'usage', 'status'])
    expect(chunks[0]).to.include({text: 'Hello'})
    expect(chunks[2]).to.deep.include({usage: {inputTokens: 5, outputTokens: 7, totalTokens: 12}})
    expect(chunks[3]).to.include({status: 'completed'})

    expect(fakeClient.responses.lastArgs).to.deep.include({
      model: 'gpt-4o-mini',
      temperature: 0.1,
    })
    expect(fakeClient.responses.lastStream?.aborted).to.equal(true)
  })

  it('emits error chunks when the stream reports response.error', async () => {
    const events: StreamEvent[] = [
      {delta: 'partial', type: 'response.output_text.delta'},
      {error: new Error('boom'), type: 'response.error'},
      {type: 'response.completed'},
    ]

    const fakeClient = new FakeOpenAI(events)
    const provider = new OpenAIProvider({apiKey: 'test-key', model: 'gpt-4o-mini'}, fakeClient as unknown as OpenAI)

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

    const lastChunk = chunks.at(-1)
    expect(lastChunk?.type).to.equal('error')
    expect(lastChunk).to.have.property('error')
  })
})
