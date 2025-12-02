import type {ReactElement} from 'react'

import {Box, Text} from 'ink'

export interface InputPanelProps {
  cursorVisible?: boolean
  disabled?: boolean
  label: string
  placeholder?: string
  value: string
}

export function InputPanel(props: InputPanelProps): ReactElement {
  const cursor = props.disabled ? '' : props.cursorVisible ? '▌' : ' '

  return (
    <Box
      borderColor="cyan"
      borderStyle="round"
      flexDirection="column"
      paddingX={1}
      paddingY={0}
      width="100%"
    >
      <Box>
        <Text color="yellow">{props.label}</Text>
        {props.value ? (
          <Text>{` ${props.value}${cursor}`}</Text>
        ) : (
          <>
            <Text>{` ${cursor}`}</Text>
            {props.placeholder ? <Text dimColor>{props.placeholder}</Text> : null}
          </>
        )}
        {props.disabled ? <Text dimColor>{' (busy)'}</Text> : null}
      </Box>
    </Box>
  )
}
