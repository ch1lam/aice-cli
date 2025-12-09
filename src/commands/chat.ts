import {Args, Command, Flags} from '@oclif/core'

import {renderStream} from '../chat/chat-runner.js'
import {ChatController} from '../chat/controller.js'
import {loadProviderEnv} from '../config/env.js'

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
      description: 'Provider ID (openai, deepseek)',
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
    const providerId = flags.provider ?? (process.env.AICE_PROVIDER as string) ?? 'openai'
    const env = loadProviderEnv({providerId})
    const controller = new ChatController({env})

    const stream = controller.createStream({
      model: flags.model,
      prompt: args.prompt,
      providerId,
      systemPrompt: flags.system,
      temperature: flags.temperature,
    })

    await renderStream(stream, {
      error: (error: Error) => this.error(error, {exit: 1}),
      log: (message: string) => this.log(message),
      write(text: string) {
        process.stdout.write(text)
      },
    })
  }
}
