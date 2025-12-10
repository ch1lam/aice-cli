import { Command } from '@oclif/core'
import { render } from 'ink'
import React from 'react'

import { tryLoadProviderEnv } from '../config/env.js'
import { AiceApp } from '../ui/aice-app.js'

export default class Tui extends Command {
  static description = 'Launch the interactive aice TUI shell.'

  async run(): Promise<void> {
    const { stdin } = process
    const supportsRaw = stdin.isTTY && typeof stdin.setRawMode === 'function'

    if (!supportsRaw) {
      this.error('TUI requires a TTY (stdin with setRawMode). Run in a real terminal.', {
        exit: 1,
      })
    }

    clearTerminal()

    const { env, error } = tryLoadProviderEnv()
    const { waitUntilExit } = render(
      React.createElement(AiceApp, { initialEnv: env, initialError: error }),
    )
    await waitUntilExit()
  }
}

function clearTerminal(): void {
  // Clear the visible screen and jump to top-left, but keep scrollback intact for debugging.
  process.stdout.write('\u001B[2J\u001B[H')
}
