import {createElement} from 'react'
import type {ReactElement} from 'react'
import {Box, Text} from 'ink'

import type {ProviderId, StreamStatus, TokenUsage} from '../core/stream.js'

export interface StatusBarProps {
  error?: Error | null
  model?: string
  providerId?: ProviderId
  status?: StreamStatus
  statusDetail?: string
  usage?: TokenUsage
}

const STATUS_COLORS: Record<StreamStatus, Text['props']['color']> = {
  completed: 'green',
  failed: 'red',
  queued: 'blue',
  running: 'yellow',
}

const DEFAULT_STATUS_LABEL = 'idle'

function formatUsageValue(value?: number): string {
  if (value === undefined || value === null) {
    return '-'
  }

  return value.toString()
}

export function StatusBar({
  error,
  model,
  providerId,
  status,
  statusDetail,
  usage,
}: StatusBarProps): ReactElement {
  const statusLabel = status ?? DEFAULT_STATUS_LABEL
  const detailLabel = statusDetail ? ` detail=${statusDetail}` : ''
  const statusColor = status ? STATUS_COLORS[status] : undefined

  const segments: ReactElement[] = [
    createElement(Text, {key: 'provider'}, `provider=${providerId ?? '-'}`),
  ]

  if (model) {
    segments.push(createElement(Text, {key: 'model'}, ` model=${model}`))
  }

  segments.push(createElement(Text, {color: statusColor, key: 'status'}, ` status=${statusLabel}${detailLabel}`))

  if (usage) {
    segments.push(
      createElement(
        Text,
        {key: 'usage'},
        ` usage in=${formatUsageValue(usage.inputTokens)} out=${formatUsageValue(usage.outputTokens)} total=${formatUsageValue(
          usage.totalTokens,
        )}`,
      ),
    )
  }

  if (error) {
    segments.push(createElement(Text, {color: 'red', key: 'error'}, ` error=${error.message}`))
  }

  return createElement(Box, {borderStyle: 'round', paddingX: 1, paddingY: 0}, ...segments)
}

export default StatusBar
