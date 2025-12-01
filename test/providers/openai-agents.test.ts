import {Agent, Runner, type RunStreamEvent} from '@openai/agents'
import {expect} from 'chai'

import type {ProviderStreamChunk} from '../../src/core/stream.ts'

import {
  OpenAIAgentsProvider,
  type OpenAIAgentsSessionRequest,
} from '../../src/providers/openai-agents.ts'

class FakeRunResult implements AsyncIterable<RunStreamEvent> {
  readonly completed: Promise<void>
  readonly error: unknown
  #events: RunStreamEvent[]

  constructor(events: RunStreamEvent[], error?: unknown) {
    this.#events = events
    this.error = error
    this.completed = Promise.resolve()
  }

  async *[Symbol.asyncIterator]() {
    for (const event of this.#events) {
      yield event
    }
  }
}

class FakeRunner {
  lastAgent?: Agent
  lastInput?: string
  lastOptions?: unknown
  result: FakeRunResult

  constructor(result: FakeRunResult) {
    this.result = result
  }

  async run(agent: Agent, input: string, options?: unknown): Promise<FakeRunResult> {
    this.lastAgent = agent
    this.lastInput = input
    this.lastOptions = options
    return this.result
  }
}

function createRunEvent(event: RunStreamEvent): RunStreamEvent {
  return event
}

describe('OpenAIAgentsProvider', () => {
  it('requires an API key', () => {
    expect(() => new OpenAIAgentsProvider({apiKey: ''})).to.throw('Missing OpenAI API key')
  })

  it('streams response events into text, usage, and status chunks', async () => {
    const events: RunStreamEvent[] = [
      createRunEvent({data: {type: 'response_started'}, type: 'raw_model_stream_event'}),
      createRunEvent({
        data: {delta: 'Hello', type: 'output_text_delta'},
        type: 'raw_model_stream_event',
      }),
      createRunEvent({
        data: {delta: ' world', type: 'output_text_delta'},
        type: 'raw_model_stream_event',
      }),
      createRunEvent({
        data: {
          response: {
            id: 'resp_123',
            output: [],
            usage: {inputTokens: 2, outputTokens: 3, totalTokens: 5},
          },
          type: 'response_done',
        },
        type: 'raw_model_stream_event',
      }),
    ]

    const fakeRunner = new FakeRunner(new FakeRunResult(events))
    const provider = new OpenAIAgentsProvider(
      {apiKey: 'key', model: 'gpt-4.1'},
      fakeRunner as unknown as Runner,
    )
    const request: OpenAIAgentsSessionRequest = {
      model: 'gpt-4.1-mini',
      prompt: 'Hello?',
      providerId: 'openai-agents',
    }

    const chunks: ProviderStreamChunk[] = []
    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    const types = chunks.map(chunk => chunk.type)
    expect(types).to.deep.equal(['status', 'text', 'text', 'usage', 'status'])
    expect(chunks[1]).to.include({text: 'Hello'})
    expect(chunks[2]).to.include({text: ' world'})
    expect(chunks[3]).to.deep.include({usage: {inputTokens: 2, outputTokens: 3, totalTokens: 5}})
    expect(chunks[4]).to.include({status: 'completed'})

    expect(fakeRunner.lastOptions).to.deep.include({stream: true})
  })

  it('passes system prompt, model, and temperature into the agent', async () => {
    const events: RunStreamEvent[] = []
    const fakeRunner = new FakeRunner(new FakeRunResult(events))
    const provider = new OpenAIAgentsProvider(
      {apiKey: 'key', instructions: 'Default instructions', model: 'gpt-4.1'},
      fakeRunner as unknown as Runner,
    )

    const request: OpenAIAgentsSessionRequest = {
      model: 'gpt-4.1-mini',
      prompt: 'Hi',
      providerId: 'openai-agents',
      systemPrompt: 'Custom instructions',
      temperature: 0.4,
    }

    for await (const chunk of provider.stream(request)) {
      expect(chunk).to.exist
    }

    expect(fakeRunner.lastAgent?.instructions).to.equal('Custom instructions')
    expect(fakeRunner.lastAgent?.model).to.equal('gpt-4.1-mini')
    expect(fakeRunner.lastAgent?.modelSettings.temperature).to.equal(0.4)
  })

  it('emits an error chunk when the run rejects', async () => {
    const fakeRunner = new FakeRunner(new FakeRunResult([], new Error('Agents failure')))
    const provider = new OpenAIAgentsProvider({apiKey: 'key'}, fakeRunner as unknown as Runner)
    const request: OpenAIAgentsSessionRequest = {
      model: 'gpt-4.1',
      prompt: 'trigger failure',
      providerId: 'openai-agents',
    }

    const chunks: ProviderStreamChunk[] = []
    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    const lastChunk = chunks.at(-1)
    expect(lastChunk?.type).to.equal('error')
    expect(lastChunk).to.have.property('error')
  })
})
