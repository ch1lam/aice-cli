import type { ProviderEnv } from '../config/env.js'
import type { SessionStream } from '../core/stream.js'
import type { ProviderBindingFactory } from '../providers/factory.js'

import { type ChatPrompt, ChatService } from '../application/chat-service.js'

export type { ChatPrompt } from '../application/chat-service.js'

export interface ChatControllerOptions {
  bindingFactory?: ProviderBindingFactory
  env: ProviderEnv
}

export class ChatController {
  #chatService: ChatService
  #env: ProviderEnv

  constructor(options: ChatControllerOptions) {
    this.#env = options.env
    this.#chatService = new ChatService({ bindingFactory: options.bindingFactory })
  }

  createStream(prompt: ChatPrompt): SessionStream {
    return this.#chatService.createStream(this.#env, prompt)
  }
}
