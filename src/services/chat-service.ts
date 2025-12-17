import type { ProviderEnv } from '../config/env.js'
import type { SessionStream } from '../core/stream.js'

import { runSession } from '../core/session.js'
import {
  createProviderBinding,
  type ProviderBindingFactory,
  type ProviderRequestInput,
} from '../providers/factory.js'

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

