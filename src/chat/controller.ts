import type {ProviderEnv} from '../config/env.js'
import type {ProviderId, SessionStream} from '../core/stream.js'

import {runSession} from '../core/session.js'
import {
  createProviderBinding,
  type ProviderBinding,
  type ProviderBindingFactory,
  type ProviderRequestInput,
} from '../providers/factory.js'

export interface ChatPrompt extends ProviderRequestInput {
  providerId?: ProviderId
}

export interface ChatControllerOptions {
  bindingFactory?: ProviderBindingFactory
  env: ProviderEnv
}

export class ChatController {
  #bindingFactory: ProviderBindingFactory
  #env: ProviderEnv

  constructor(options: ChatControllerOptions) {
    this.#env = options.env
    this.#bindingFactory = options.bindingFactory ?? (opts => createProviderBinding(opts))
  }

  createStream(prompt: ChatPrompt): SessionStream {
    const providerId = prompt.providerId ?? this.#env.providerId
    this.#assertProvider(providerId)

    const binding = this.#bindingFactory({env: this.#env, providerId}) as ProviderBinding
    const request = binding.createRequest(prompt)

    return runSession(binding.provider, request)
  }

  #assertProvider(requestedId: ProviderId): void {
    if (requestedId !== this.#env.providerId) {
      throw new Error(
        `Configured provider ${this.#env.providerId} does not match requested ${requestedId}`,
      )
    }
  }
}
