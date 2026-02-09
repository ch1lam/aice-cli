import {
  type LanguageModel,
  type ModelMessage,
  type TextStreamPart,
  type ToolLoopAgentSettings,
  type ToolSet,
} from 'ai'
import { tool } from 'ai'
import { expect } from 'chai'
import { z } from 'zod'

import type { ProviderStreamChunk } from '../../src/types/stream.ts'

import { DeepSeekProvider, type DeepSeekSessionRequest } from '../../src/providers/deepseek.ts'
import { createUsage } from '../helpers/usage.ts'

type StreamPart = TextStreamPart<ToolSet>

type AgentStreamOptions = {
  abortSignal?: AbortSignal
  messages: ModelMessage[]
}

type AgentStreamResult = {
  fullStream: AsyncIterable<StreamPart>
}

type AgentLike = {
  stream: (options: AgentStreamOptions) => PromiseLike<AgentStreamResult>
}

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

    const tools = {
      'mock_tool': tool({
        description: 'Test tool',
        async execute() {
          return 'ok'
        },
        inputSchema: z.object({}),
      }),
    }

    let capturedAgentSettings: ToolLoopAgentSettings<never, ToolSet> | undefined
    let capturedStreamOptions: AgentStreamOptions | undefined
    const agentFactory = (settings: ToolLoopAgentSettings<never, ToolSet>): AgentLike => {
      capturedAgentSettings = settings

      return {
        async stream(options) {
          capturedStreamOptions = options
          return { fullStream: new FakeStream(events) }
        },
      }
    }

    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      { agentFactory, modelFactory, toolsFactory: () => tools },
    )

    const controller = new AbortController()
    const messages: ModelMessage[] = [
      { content: 'You are helpful', role: 'system' },
      { content: 'Hello?', role: 'user' },
    ]
    const request: DeepSeekSessionRequest = {
      messages,
      model: 'deepseek-chat',
      providerId: 'deepseek',
      signal: controller.signal,
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
    expect(capturedStreamOptions).to.include({
      abortSignal: controller.signal,
      messages,
    })
    expect(capturedAgentSettings).to.include({
      temperature: 0.2,
      tools,
    })
    expect(capturedAgentSettings?.instructions).to.contain('AICE')
    expect(capturedAgentSettings?.model).to.deep.equal({
      modelId: 'deepseek-chat',
    })
  })

  it('forwards error parts from the stream', async () => {
    const events: Array<Error | StreamPart> = [
      { id: 'txt-0', text: 'partial', type: 'text-delta' },
      { error: new Error('DeepSeek response failed'), type: 'error' },
    ]

    const agentFactory = (): AgentLike => ({
      async stream() {
        return { fullStream: new FakeStream(events) }
      },
    })

    const provider = new DeepSeekProvider(
      { apiKey: 'deepseek-key', model: 'deepseek-chat' },
      { agentFactory, modelFactory: () => ({}) as LanguageModel },
    )

    const request: DeepSeekSessionRequest = {
      messages: [{ content: 'trigger', role: 'user' }],
      model: 'deepseek-chat',
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
