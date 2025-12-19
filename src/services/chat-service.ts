import type { SessionStream } from '../core/stream.js'
import type { ProviderEnv } from '../types/env.js'
import type { ProviderBindingFactory, ProviderRequestInput } from '../types/provider.js'

import { runSession } from '../core/session.js'
import { createProviderBinding } from '../providers/factory.js'

export type ChatPrompt = ProviderRequestInput

export interface ChatServiceOptions {
  bindingFactory?: ProviderBindingFactory
}

export class ChatService {
  #bindingFactory: ProviderBindingFactory

  constructor(options: ChatServiceOptions = {}) {
    this.#bindingFactory = options.bindingFactory ?? (opts => createProviderBinding(opts))
  }

  createStream(env: ProviderEnv, prompt: ChatPrompt): SessionStream {
    const binding = this.#bindingFactory({ env, providerId: env.providerId })
    const request = binding.createRequest(prompt)

    return runSession(binding.provider, request)
  }
}
