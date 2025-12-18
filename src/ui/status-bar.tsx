import { Box, Text, useStdout } from 'ink'
import { type ReactElement, useEffect, useState } from 'react'

import type { ProviderId, StreamStatus, TokenUsage } from '../core/stream.js'

import { theme } from './theme.js'

const statusSpinnerFrames = ['-', '\\', '|', '/']

export interface StatusBarProps {
  meta?: {
    model: string
    providerId: ProviderId
  }
  status?: StreamStatus
  statusMessage?: string
  usage?: TokenUsage
}

export function StatusBar(props: StatusBarProps): ReactElement {
  const { meta, status, statusMessage, usage } = props
  const { stdout } = useStdout()
  const [columns, setColumns] = useState<number | undefined>(stdout?.columns)
  const barWidth =
    typeof columns === 'number' && Number.isFinite(columns) && columns > 0 ? columns : 80

  useEffect(() => {
    if (!stdout) return

    const handleResize = () => {
      setColumns(stdout.columns)
    }

    handleResize()
    stdout.on('resize', handleResize)
    return () => {
      stdout.off('resize', handleResize)
    }
  }, [stdout])
  const providerText = meta ? `${meta.providerId}:${meta.model}` : 'provider:-'
  const isActive = status === 'running' || status === 'queued'
  const colors = theme.components.statusBar
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

  const statusLabel = status ? `status:${status}` : 'status:pending'
  const statusColor = status === 'failed' ? colors.error : colors.status
  const statusText = isActive
    ? `${statusLabel} ${statusSpinnerFrames[spinnerIndex]}`
    : statusLabel
  const usageText = formatUsage(usage)

  return (
    <Box alignItems="center" justifyContent="space-between" paddingX={1} width={barWidth}>
      <Box flexShrink={1}>
        <Text color={colors.provider}>{providerText}</Text>
        <Text color={colors.separator}>{'  |  '}</Text>
        <Text backgroundColor={theme.semantic.active} bold color={statusColor}>
          {` ${statusText} `}
        </Text>
        {statusMessage ? (
          <Text color={statusColor} wrap="truncate">
            {` - ${statusMessage}`}
          </Text>
        ) : null}
      </Box>
      <Text color={colors.usage} wrap="truncate">
        {usageText}
      </Text>
    </Box>
  )
}

function formatUsage(usage?: TokenUsage): string {
  const input = usage?.inputTokens ?? '-'
  const output = usage?.outputTokens ?? '-'
  const total = usage?.totalTokens ?? '-'
  return `usage in=${input} out=${output} total=${total}`
}
