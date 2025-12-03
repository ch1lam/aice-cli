import type {ReactElement} from 'react'

import {Box, Text} from 'ink'

import {theme} from './theme.js'

export interface SlashSuggestion {
  command: string
  description: string
  hint?: string
  value: string
}

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
    <Box
      borderColor={colors.border}
      borderStyle="round"
      flexDirection="column"
      paddingX={1}
      paddingY={0}
      width="100%"
    >
      <Box marginBottom={1}>
        <Text color={colors.helper} dimColor>
          Use ↑/↓ to browse, Tab to autocomplete, Enter to send the highlighted command.
        </Text>
      </Box>
      {props.items.map((item, itemIndex) => {
        const isActive = itemIndex === index
        const indicator = isActive ? '>' : ' '

        return (
          <Box flexDirection="column" key={item.value}>
            <Box>
              <Text color={isActive ? colors.selected : colors.command}>{`${indicator} ${withLeadingSlash(item.value)}`}</Text>
              {item.hint ? (
                <Text color={colors.hint}>{` ${item.hint}`}</Text>
              ) : null}
            </Box>
            <Box marginLeft={2}>
              <Text color={colors.description} dimColor>
                {item.description}
              </Text>
            </Box>
          </Box>
        )
      })}
    </Box>
  )
}

function clampIndex(index: number, length: number): number {
  if (length === 0) return 0
  if (index < 0) return 0
  if (index >= length) return length - 1
  return index
}

function withLeadingSlash(value: string): string {
  if (!value) return '/'
  return value.startsWith('/') ? value : `/${value}`
}
