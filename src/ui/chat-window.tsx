import type { ReactElement } from 'react'

import { Box, Text } from 'ink'

import { theme } from './theme.js'

export interface ChatWindowProps {
  content: string
  prompt?: string
}

export function ChatWindow(props: ChatWindowProps): ReactElement {
  const content = props.content.trim() ? props.content : 'Waiting for the model...'
  const colors = theme.components.chatWindow

  return (
    <Box flexDirection="column">
      {props.prompt ? (
        <Text color={colors.userLabel}>{`You: ${props.prompt}`}</Text>
      ) : null}
      <Text color={colors.assistantLabel}>Assistant:</Text>
      <Text color={colors.content} wrap="wrap">
        {content}
      </Text>
    </Box>
  )
}
