import type {ReactElement} from 'react'

import {Box, Text} from 'ink'

export interface ChatWindowProps {
  content: string
  prompt?: string
}

export function ChatWindow(props: ChatWindowProps): ReactElement {
  const content = props.content.trim() ? props.content : 'Waiting for the model...'

  return (
    <Box flexDirection="column">
      {props.prompt ? (
        <Text color="magenta">{`You: ${props.prompt}`}</Text>
      ) : null}
      <Text color="cyan">Assistant:</Text>
      <Text wrap="wrap">{content}</Text>
    </Box>
  )
}
