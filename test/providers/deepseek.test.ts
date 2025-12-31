import type { LanguageModel, TextStreamPart, ToolSet } from 'ai'

import { expect } from 'chai'

import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { DeepSeekProvider, type DeepSeekSessionRequest } from '../../src/providers/deepseek.ts'
import { createUsage } from '../helpers/usage.ts'

type StreamPart = TextStreamPart<ToolSet>

type StreamTextOptions = {
  abortSignal?: AbortSignal
  model: LanguageModel
  prompt: string
  system?: string
  temperature?: number
}

type StreamTextFn = (options: StreamTextOptions) => { fullStream: AsyncIterable<StreamPart> }

class FakeStream implements AsyncIterable<StreamPart> {
  private readonly events: Array<Error | StreamPart>

  constructor(events: Array<Error | StreamPart>) {
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

describe('DeepSeekProvider', () => {
  it('rejects missing API keys', () => {
    expect(() => new DeepSeekProvider({ apiKey: '' })).to.throw('Missing DeepSeek API key')
  })

  it('streams text parts in order and forwards request options', async () => {
    const events: Array<Error | StreamPart> = [
      { type: 'start' },
      { id: 'txt-0', text: 'Hello', type: 'text-delta' },
      { id: 'reason-0', text: ' thinking', type: 'reasoning-delta' },
      { id: 'txt-0', text: ' world', type: 'text-delta' },
      {
        finishReason: 'stop',
        rawFinishReason: 'stop',
        totalUsage: createUsage({ inputTokens: 2, outputTokens: 3, totalTokens: 5 }),
        type: 'finish',
      },
    ]

    const modelCalls: string[] = []
    const modelFactory = (modelId: string) => {
      modelCalls.push(modelId)
      return { modelId } as LanguageModel
    }

    let capturedOptions: StreamTextOptions | undefined
    const streamText: StreamTextFn = options => {
      capturedOptions = options
      return { fullStream: new FakeStream(events) }
    }

    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      { modelFactory, streamText },
    )

    const controller = new AbortController()
    const request: DeepSeekSessionRequest = {
      model: 'deepseek-chat',
      prompt: 'Hello?',
      providerId: 'deepseek',
      signal: controller.signal,
      systemPrompt: 'You are helpful',
      temperature: 0.2,
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    expect(chunks.map(chunk => chunk.type)).to.deep.equal([
      'start',
      'text-delta',
      'reasoning-delta',
      'text-delta',
      'finish',
    ])
    const finish = chunks.find(
      (chunk): chunk is Extract<ProviderStreamChunk, { type: 'finish' }> =>
        chunk.type === 'finish',
    )
    expect(finish, 'finish chunk').to.not.equal(undefined)
    if (finish) {
      expect(finish.totalUsage).to.include({
        inputTokens: 2,
        outputTokens: 3,
        totalTokens: 5,
      })
    }

    expect(modelCalls).to.deep.equal(['deepseek-chat'])
    expect(capturedOptions).to.include({
      abortSignal: controller.signal,
      prompt: 'Hello?',
      system: 'You are helpful',
      temperature: 0.2,
    })
  })

  it('forwards error parts from the stream', async () => {
    const events: Array<Error | StreamPart> = [
      { id: 'txt-0', text: 'partial', type: 'text-delta' },
      { error: new Error('DeepSeek response failed'), type: 'error' },
    ]

    const streamText: StreamTextFn = () => ({ fullStream: new FakeStream(events) })

    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      { modelFactory: () => ({}) as LanguageModel, streamText },
    )

    const request: DeepSeekSessionRequest = {
      model: 'deepseek-chat',
      prompt: 'trigger',
      providerId: 'deepseek',
    }

    const chunks: ProviderStreamChunk[] = []

    for await (const chunk of provider.stream(request)) {
      chunks.push(chunk)
    }

    expect(chunks.map(chunk => chunk.type)).to.deep.equal(['text-delta', 'error'])
    const last = chunks.at(-1)
    if (last?.type === 'error') {
      expect(last.error.message).to.contain('DeepSeek response failed')
    }
  })
})
