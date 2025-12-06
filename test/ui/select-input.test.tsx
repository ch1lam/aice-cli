import { expect } from 'chai'
import { render } from 'ink-testing-library'

import { SelectInput } from '../../src/ui/select-input.js'

function stripAnsi(value: string): string {
  const escape = '\u001B['
  let cursor = 0
  let output = ''

  while (cursor < value.length) {
    const next = value.indexOf(escape, cursor)
    if (next === -1) {
      output += value.slice(cursor)
      break
    }

    output += value.slice(cursor, next)
    const end = value.indexOf('m', next)
    if (end === -1) break

    cursor = end + 1
  }

  return output
}

describe('SelectInput', () => {
  it('highlights the selected item and shows usage instructions', () => {
    const {lastFrame} = render(
      <SelectInput
        active
        items={[
          {description: 'default', label: 'OpenAI', value: 'openai'},
          {label: 'DeepSeek', value: 'deepseek'},
        ]}
        selectedIndex={1}
        title="Provider"
      />,
    )

    const frame = stripAnsi(lastFrame() ?? '')

    expect(frame).to.include('Provider')
    expect(frame).to.include('OpenAI (openai)')
    expect(frame).to.include('> DeepSeek (deepseek)')
    expect(frame).to.include('Use arrow keys to choose, Enter to confirm.')
  })
})
