import {Args, Command, Flags} from '@oclif/core'

import {loadProviderEnv} from '../config/env.js'
import {runSession} from '../core/session.js'
import {OpenAIProvider} from '../providers/openai.js'
import {renderStream} from './chat-runner.js'

export default class Chat extends Command {
  static args = {
    prompt: Args.string({
      description: 'User prompt text',
      required: true,
    }),
  }
  static description = 'Stream a single-turn chat completion to stdout'
  static flags = {
    model: Flags.string({
      char: 'm',
      description: 'Model identifier',
    }),
    provider: Flags.string({
      char: 'p',
      default: 'openai',
      description: 'Provider ID (currently openai only)',
    }),
    system: Flags.string({
      char: 's',
      description: 'System prompt text',
    }),
    temperature: Flags.integer({
      char: 't',
      description: 'Sampling temperature (0-2)',
    }),
  }

  async run(): Promise<void> {
    const {args, flags} = await this.parse(Chat)
    const env = loadProviderEnv()

    if (flags.provider !== env.providerId) {
      throw new Error(`Only ${env.providerId} provider is available right now`)
    }

    const providerConfig = {
      apiKey: env.apiKey,
      baseURL: env.baseURL,
      model: env.model ?? flags.model,
    }
    const provider = new OpenAIProvider(providerConfig)

    const request = {
      model: flags.model ?? env.model ?? 'gpt-4o-mini',
      prompt: args.prompt,
      providerId: provider.id,
      systemPrompt: flags.system,
      temperature: flags.temperature,
    }

    const stream = runSession(provider, request)

    await renderStream(stream, {
      error: error => this.error(error, {exit: 1}),
      log: message => this.log(message),
      write(text) {
        process.stdout.write(text)
      },
    })
  }
}
