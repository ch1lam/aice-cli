#!/usr/bin/env -S node --loader ts-node/esm --disable-warning=ExperimentalWarning

import {execute} from '@oclif/core'

if (process.argv.length <= 2) {
  process.argv.push('tui')
}

await execute({development: true, dir: import.meta.url})
