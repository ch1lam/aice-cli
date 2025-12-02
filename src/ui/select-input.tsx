import {Box, Text} from 'ink'
import {type ReactElement} from 'react'

import {theme} from './theme.js'

export interface SelectInputItem<T extends string> {
  description?: string
  label: string
  value: T
}

export interface SelectInputProps<T extends string> {
  active?: boolean
  items: Array<SelectInputItem<T>>
  selectedIndex: number
  title?: string
}

export function SelectInput<T extends string>(props: SelectInputProps<T>): ReactElement {
  const safeIndex = clampIndex(props.selectedIndex, props.items.length)
  const colors = theme.components.selectInput

  return (
    <Box
      borderColor={props.active ? colors.activeBorder : colors.border}
      borderStyle="round"
      flexDirection="column"
      paddingX={1}
      paddingY={0}
      width="100%"
    >
      {props.title ? (
        <Box marginBottom={1}>
          <Text color={colors.title}>{props.title}</Text>
        </Box>
      ) : null}
      {props.items.map((item, index) => {
        const isSelected = index === safeIndex
        const indicator = isSelected ? '>' : ' '

        return (
          <Box key={item.value}>
            <Text color={isSelected ? colors.selected : colors.label}>{`${indicator} ${item.label}`}</Text>
            <Text color={colors.value}>{` (${item.value})`}</Text>
            {item.description ? (
              <Text color={colors.helper} dimColor>{` · ${item.description}`}</Text>
            ) : null}
          </Box>
        )
      })}
      {props.active ? (
        <Box marginTop={1}>
          <Text color={colors.helper} dimColor>
            Use arrow keys to choose, Enter to confirm.
          </Text>
        </Box>
      ) : null}
    </Box>
  )
}

function clampIndex(index: number, length: number): number {
  if (length === 0) return 0
  if (index < 0) return 0
  if (index >= length) return length - 1
  return index
}
