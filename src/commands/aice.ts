import { Command } from '@oclif/core'

import { runTui } from '../ui/run-tui.js'

export default class Aice extends Command {
  static description = 'Launch the interactive aice TUI shell.'

  async run(): Promise<void> {
    assertTuiSupported(this)
    await runTui()
  }
}

function assertTuiSupported(command: Command): void {
  const { stdin } = process
  const supportsRaw = stdin.isTTY && typeof stdin.setRawMode === 'function'

  if (!supportsRaw) {
    command.error('TUI requires a TTY (stdin with setRawMode). Run in a real terminal.', {
      exit: 1,
    })
  }
}
