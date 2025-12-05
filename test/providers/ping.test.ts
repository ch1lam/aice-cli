import {expect} from 'chai'

import {pingProvider} from '../../src/providers/ping.ts'

class FakeOpenAIModelsClient {
  calls: string[] = []
  shouldFail = false
  shouldHang = false

  async retrieve(model: string): Promise<void> {
    if (this.shouldHang) {
      return new Promise(() => {})
    }

    if (this.shouldFail) {
      throw new Error('bad auth')
    }

    this.calls.push(model)
  }
}

class FakeOpenAIClient {
  models: FakeOpenAIModelsClient

  constructor(models: FakeOpenAIModelsClient) {
    this.models = models
  }
}

class FakeAnthropicModelsClient {
  called = 0

  async list(): Promise<void> {
    this.called++
  }
}

class FakeAnthropicClient {
  models: FakeAnthropicModelsClient

  constructor(models: FakeAnthropicModelsClient) {
    this.models = models
  }
}

describe('pingProvider', () => {
  it('pings OpenAI using the default model when none is provided', async () => {
    const models = new FakeOpenAIModelsClient()
    const client = new FakeOpenAIClient(models)

    await pingProvider({apiKey: 'key', providerId: 'openai'}, {clients: {openai: client}})

    expect(models.calls).to.deep.equal(['gpt-4o-mini'])
  })

  it('pings OpenAI Agents using the default agent model', async () => {
    const models = new FakeOpenAIModelsClient()
    const client = new FakeOpenAIClient(models)

    await pingProvider({apiKey: 'key', providerId: 'openai-agents'}, {clients: {openai: client}})

    expect(models.calls).to.deep.equal(['gpt-4.1'])
  })

  it('pings DeepSeek using the default model', async () => {
    const models = new FakeOpenAIModelsClient()
    const client = new FakeOpenAIClient(models)

    await pingProvider({apiKey: 'key', providerId: 'deepseek'}, {clients: {deepseek: client}})

    expect(models.calls).to.deep.equal(['deepseek-chat'])
  })

  it('pings Anthropic by listing models', async () => {
    const models = new FakeAnthropicModelsClient()
    const client = new FakeAnthropicClient(models)

    await pingProvider({apiKey: 'key', providerId: 'anthropic'}, {clients: {anthropic: client}})

    expect(models.called).to.equal(1)
  })

  it('surfaces connectivity failures', async () => {
    const models = new FakeOpenAIModelsClient()
    models.shouldFail = true
    const client = new FakeOpenAIClient(models)
    let error: unknown

    try {
      await pingProvider({apiKey: 'bad', providerId: 'openai'}, {clients: {openai: client}})
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('bad auth')
  })

  it('times out when the provider call hangs', async () => {
    const models = new FakeOpenAIModelsClient()
    models.shouldHang = true
    const client = new FakeOpenAIClient(models)
    let error: unknown

    try {
      await pingProvider(
        {apiKey: 'key', providerId: 'openai'},
        {clients: {openai: client}, timeoutMs: 10},
      )
    } catch (error_) {
      error = error_
    }

    expect(error).to.be.instanceOf(Error)
    expect((error as Error).message).to.equal('Connectivity check timed out')
  })
})
