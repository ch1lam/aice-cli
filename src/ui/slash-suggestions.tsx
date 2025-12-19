import type { ReactElement } from 'react'

import { Box, Text } from 'ink'

import type { SlashSuggestion } from '../types/slash-suggestions.js'

import { theme } from './theme.js'
import { clampIndex } from './utils.js'

export interface SlashSuggestionsProps {
  activeIndex?: number
  items: SlashSuggestion[]
  visible?: boolean
}

export function SlashSuggestions(props: SlashSuggestionsProps): null | ReactElement {
  if (!props.visible || props.items.length === 0) return null

  const index = clampIndex(props.activeIndex ?? 0, props.items.length)
  const colors = theme.components.slashSuggestions

  return (
    <Box flexDirection="column" paddingX={1} paddingY={0} width="100%">
      {props.items.map((item, itemIndex) => {
        const isActive = itemIndex === index
        const indicator = isActive ? '•' : ' '
        const commandText = withLeadingSlash(item.hint ?? item.value)

        return (
          <Box key={item.value}>
            <Text color={isActive ? colors.selected : colors.command}>
              {`${indicator} ${commandText}`}
            </Text>
            <Text color={colors.description} dimColor>
              {`  ${item.description}`}
            </Text>
          </Box>
        )
      })}
      <Box>
        <Text color={colors.helper} dimColor>
          Use ↑/↓ to browse, Tab to autocomplete, Enter to send the highlighted command.
        </Text>
      </Box>
    </Box>
  )
}

function withLeadingSlash(value: string): string {
  if (!value) return '/'
  return value.startsWith('/') ? value : `/${value}`
}
