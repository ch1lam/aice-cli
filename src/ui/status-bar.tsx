import {Box, Text} from 'ink'
import {type ReactElement, useEffect, useState} from 'react'

import type {StreamStatus, TokenUsage} from '../core/stream.js'

const statusSpinnerFrames = ['-', '\\', '|', '/']

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
  const isActive = props.status === 'running' || props.status === 'queued'
  const [spinnerIndex, setSpinnerIndex] = useState(0)

  useEffect(() => {
    if (!isActive) {
      setSpinnerIndex(0)
      return
    }

    const timer = setInterval(() => {
      setSpinnerIndex(current => (current + 1) % statusSpinnerFrames.length)
    }, 120)

    return () => {
      clearInterval(timer)
    }
  }, [isActive])

  const statusLabel = props.status ? `status:${props.status}` : 'status:pending'
  const statusText = isActive
    ? `${statusLabel} ${statusSpinnerFrames[spinnerIndex]}`
    : statusLabel
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
