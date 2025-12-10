import type { ReactElement } from 'react'

import { Box, Text } from 'ink'

import { theme } from './theme.js'

export interface InputPanelProps {
  cursorVisible?: boolean
  disabled?: boolean
  label: string
  placeholder?: string
  value: string
}

export function InputPanel(props: InputPanelProps): ReactElement {
  const cursor = props.disabled ? '' : props.cursorVisible ? '▌' : ' '
  const colors = theme.components.inputPanel
  const borderColor = props.disabled ? colors.activeBorder : colors.border

  return (
    <Box
      borderColor={borderColor}
      borderStyle="round"
      flexDirection="column"
      paddingX={1}
      paddingY={0}
      width="100%"
    >
      <Box>
        <Text color={colors.label}>{props.label}</Text>
        {props.value ? (
          <Text color={colors.text}>{` ${props.value}${cursor}`}</Text>
        ) : (
          <>
            <Text color={colors.text}>{` ${cursor}`}</Text>
            {props.placeholder ? (
              <Text color={colors.placeholder} dimColor>
                {props.placeholder}
              </Text>
            ) : null}
          </>
        )}
        {props.disabled ? (
          <Text color={colors.disabled} dimColor>
            {' (busy)'}
          </Text>
        ) : null}
      </Box>
    </Box>
  )
}
