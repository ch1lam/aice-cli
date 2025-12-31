import { expect } from 'chai'

import type { LLMProvider, SessionRequest } from '../../src/core/session.ts'

import { ChatService } from '../../src/services/chat-service.ts'
import { createUsage } from '../helpers/usage.ts'

function createProvider(): LLMProvider {
  return {
    id: 'deepseek',
    async *stream() {
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
  it('creates session streams through the binding factory', async () => {
    const provider = createProvider()
    const inputs: unknown[] = []
    let capturedRequest: SessionRequest | undefined
    let capturedEnvProviderId: string | undefined
    let capturedBindingProviderId: string | undefined

    const env = {
      apiKey: 'key',
      providerId: 'deepseek' as const,
    }

    const service = new ChatService({
      bindingFactory(options) {
        capturedBindingProviderId = options.providerId
        capturedEnvProviderId = options.env.providerId

        return {
          createRequest(input) {
            inputs.push(input)
            capturedRequest = {
              model: 'deepseek-chat',
              prompt: input.prompt,
              providerId: provider.id,
              systemPrompt: input.systemPrompt,
            }
            return capturedRequest
          },
          provider,
        }
      },
    })

    const stream = service.createStream(env, {
      prompt: 'Hello',
      systemPrompt: 'You are helpful',
      temperature: 1,
    })

    const types: string[] = []
    for await (const chunk of stream) {
      types.push(chunk.type)
    }

    expect(inputs).to.have.lengthOf(1)
    expect(inputs[0]).to.include({ prompt: 'Hello', systemPrompt: 'You are helpful', temperature: 1 })
    expect(capturedRequest).to.exist
    expect(capturedEnvProviderId).to.equal('deepseek')
    expect(capturedBindingProviderId).to.equal('deepseek')
    expect(types).to.deep.equal(['meta', 'text-delta', 'finish'])
  })
})
