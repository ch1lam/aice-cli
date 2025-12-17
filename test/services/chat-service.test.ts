import { expect } from 'chai'

import type { LLMProvider, SessionRequest } from '../../src/core/session.ts'

import { ChatService } from '../../src/services/chat-service.ts'

function createProvider(): LLMProvider {
  return {
    id: 'openai',
    async *stream() {
      yield { text: 'hi', type: 'text' }
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
      providerId: 'openai' as const,
    }

    const service = new ChatService({
      bindingFactory(options) {
        capturedBindingProviderId = options.providerId
        capturedEnvProviderId = options.env.providerId

        return {
          createRequest(input) {
            inputs.push(input)
            capturedRequest = {
              model: 'test-model',
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
    expect(capturedEnvProviderId).to.equal('openai')
    expect(capturedBindingProviderId).to.equal('openai')
    expect(types).to.deep.equal(['meta', 'text', 'done'])
  })
})
