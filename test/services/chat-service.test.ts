import { expect } from 'chai'

import type { LLMProvider, SessionRequest } from '../../src/types/session.ts'

import { ChatService } from '../../src/services/chat-service.ts'
import { createUsage } from '../helpers/usage.ts'

function createProvider(captureRequest: (request: SessionRequest) => void): LLMProvider {
  return {
    id: 'deepseek',
    async *stream(request) {
      captureRequest(request)
      yield { id: 'text-0', text: 'hi', type: 'text-delta' }
      yield {
        finishReason: 'stop',
        rawFinishReason: 'stop',
        totalUsage: createUsage({ inputTokens: 1, outputTokens: 1, totalTokens: 2 }),
        type: 'finish',
      }
    },
  }
}

describe('ChatService', () => {
  it('creates session streams through the provider factory', async () => {
    let capturedRequest: SessionRequest | undefined
    let capturedEnvProviderId: string | undefined

    const env = {
      apiKey: 'key',
      providerId: 'deepseek' as const,
    }

    const service = new ChatService({
      createProvider(options) {
        capturedEnvProviderId = options.providerId
        return createProvider(request => {
          capturedRequest = request
        })
      },
    })

    const messages = [
      { content: 'You are helpful', role: 'system' },
      { content: 'Hello', role: 'user' },
    ]

    const stream = service.createStream(env, {
      messages,
      temperature: 1,
    })

    const types: string[] = []
    for await (const chunk of stream) {
      types.push(chunk.type)
    }

    expect(capturedRequest).to.exist
    expect(capturedRequest).to.include({
      messages,
      model: 'deepseek-chat',
      providerId: 'deepseek',
      temperature: 1,
    })
    expect(capturedEnvProviderId).to.equal('deepseek')
    expect(types).to.deep.equal(['text-delta', 'finish'])
  })

  it('throws when the provider id mismatches the env', () => {
    const service = new ChatService({
      createProvider() {
        return {
          id: 'other',
          async *stream() {
            yield { id: 'text-0', text: 'hi', type: 'text-delta' }
          },
        }
      },
    })

    const env = { apiKey: 'key', providerId: 'deepseek' as const }

    expect(() =>
      service.createStream(env, {
        messages: [{ content: 'Hello', role: 'user' }],
      }),
    ).to.throw('Provider mismatch: expected deepseek, got other')
  })
})
