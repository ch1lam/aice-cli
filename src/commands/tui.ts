import {Command} from '@oclif/core'
import {render} from 'ink'
import React from 'react'

import {tryLoadProviderEnv} from '../config/env.js'
import {AiceApp} from '../ui/aice-app.js'

export default class Tui extends Command {
  static description = 'Launch the interactive aice TUI shell.'

  async run(): Promise<void> {
    const {stdin} = process
    const supportsRaw = stdin.isTTY && typeof stdin.setRawMode === 'function'

    if (!supportsRaw) {
      this.error('TUI requires a TTY (stdin with setRawMode). Run in a real terminal.', {
        exit: 1,
      })
    }

    const {env, error} = tryLoadProviderEnv()
    const {waitUntilExit} = render(
      React.createElement(AiceApp, {initialEnv: env, initialError: error}),
    )
    await waitUntilExit()
  }
}
