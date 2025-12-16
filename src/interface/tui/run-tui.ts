import { render } from 'ink'
import React from 'react'

import { tryLoadProviderEnv } from '../../config/env.js'
import { AiceApp } from '../../ui/aice-app.js'

export async function runTui(): Promise<void> {
  clearTerminal()

  const { env, error } = tryLoadProviderEnv()
  const { waitUntilExit } = render(
    React.createElement(AiceApp, { initialEnv: env, initialError: error }),
  )
  await waitUntilExit()
}

function clearTerminal(stdout: NodeJS.WritableStream = process.stdout): void {
  // Clear the visible screen and jump to top-left, but keep scrollback intact for debugging.
  stdout.write('\u001B[2J\u001B[H')
}

