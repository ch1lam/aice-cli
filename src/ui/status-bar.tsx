import type {ReactElement} from 'react'

import {Box, Text} from 'ink'

import type {StreamStatus, TokenUsage} from '../core/stream.js'

export interface StatusBarProps {
  meta?: {
    model: string
    providerId: string
  }
  status?: StreamStatus
  usage?: TokenUsage
}

export function StatusBar(props: StatusBarProps): ReactElement {
  const providerText = props.meta
    ? `${props.meta.providerId}:${props.meta.model}`
    : 'provider:-'
  const statusText = props.status ?? 'status:pending'
  const usageText = formatUsage(props.usage)

  return (
    <Box>
      <Text color="green">{providerText}</Text>
      <Text>{' | '}</Text>
      <Text color="yellow">{statusText}</Text>
      <Text>{' | '}</Text>
      <Text color="blue">{usageText}</Text>
    </Box>
  )
}

function formatUsage(usage?: TokenUsage): string {
  const input = usage?.inputTokens ?? '-'
  const output = usage?.outputTokens ?? '-'
  const total = usage?.totalTokens ?? '-'
  return `usage in=${input} out=${output} total=${total}`
}
