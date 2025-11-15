import {createElement, useMemo} from 'react'
import type {ReactElement} from 'react'
import {Box, Text} from 'ink'

import type {SessionTextChunk} from '../core/stream.js'

export interface ChatWindowProps {
  prompt: string
  responseChunks: SessionTextChunk[]
  title?: string
}

export function ChatWindow({prompt, responseChunks, title}: ChatWindowProps): ReactElement {
  const assistantText = useMemo(() => responseChunks.map(chunk => chunk.text).join(''), [responseChunks])

  const titleNode = title
    ? createElement(
        Box,
        {marginBottom: 1},
        createElement(Text, {color: 'gray'}, title),
      )
    : null

  const promptNode = createElement(
    Box,
    {flexDirection: 'column', marginBottom: 1},
    createElement(Text, {color: 'cyan'}, 'You'),
    createElement(Text, {wrap: 'wrap'}, prompt),
  )

  const responseNode = createElement(
    Box,
    {flexDirection: 'column'},
    createElement(Text, {color: 'green'}, 'Assistant'),
    createElement(Text, {wrap: 'wrap'}, assistantText || '…'),
  )

  return createElement(Box, {flexDirection: 'column', borderStyle: 'round', paddingX: 1, paddingY: 0}, titleNode, promptNode, responseNode)
}

export default ChatWindow
