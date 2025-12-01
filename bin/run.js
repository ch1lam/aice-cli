#!/usr/bin/env node

import {execute} from '@oclif/core'

if (process.argv.length <= 2) {
  process.argv.push('tui')
}

await execute({dir: import.meta.url})
