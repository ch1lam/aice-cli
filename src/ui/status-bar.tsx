import {Box, Text, useStdout} from 'ink'
import {type ReactElement, useEffect, useState} from 'react'

import type {StreamStatus, TokenUsage} from '../core/stream.js'

import {theme} from './theme.js'

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
  const {stdout} = useStdout()
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
  const providerText = props.meta
    ? `${props.meta.providerId}:${props.meta.model}`
    : 'provider:-'
  const isActive = props.status === 'running' || props.status === 'queued'
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

  const statusLabel = props.status ? `status:${props.status}` : 'status:pending'
  const statusColor = props.status === 'failed' ? colors.error : colors.status
  const statusText = isActive
    ? `${statusLabel} ${statusSpinnerFrames[spinnerIndex]}`
    : statusLabel
  const usageText = formatUsage(props.usage)

  return (
    <Box alignItems="center" justifyContent="space-between" paddingX={1} width={barWidth}>
      <Box flexShrink={1}>
        <Text color={colors.provider}>{providerText}</Text>
        <Text color={colors.separator}>{'  |  '}</Text>
        <Text backgroundColor={theme.semantic.active} bold color={statusColor}>
          {` ${statusText} `}
        </Text>
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
