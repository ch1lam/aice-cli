import type { ProviderEnv } from '../config/env.js'
import type { SessionStream } from '../core/stream.js'

import { runSession } from '../core/session.js'
import {
  createProviderBinding,
  type ProviderBindingFactory,
  type ProviderRequestInput,
} from '../providers/factory.js'

export type ChatPrompt = ProviderRequestInput

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
    const env = this.#env
    const { providerId } = env
    const binding = this.#bindingFactory({ env, providerId })
    const request = binding.createRequest(prompt)

    return runSession(binding.provider, request)
  }
}
