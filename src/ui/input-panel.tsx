import type { ReactElement } from 'react'

import { Box, Text, useStdout } from 'ink'
import { useEffect, useMemo, useState } from 'react'
import stringWidth from 'string-width'

import { theme } from './theme.js'
import { truncateByWidth, wrapByWidth } from './utils.js'

const DEFAULT_COLUMNS = 80
const BORDER_WIDTH = 2
const PADDING_X = 1
const DEFAULT_MAX_LINES = 6

export interface InputPanelProps {
  cursorVisible?: boolean
  disabled?: boolean
  label: string
  maxLines?: number
  placeholder?: string
  value: string
}

export function InputPanel(props: InputPanelProps): ReactElement {
  const { stdout } = useStdout()
  const [columns, setColumns] = useState<number | undefined>(stdout?.columns)
  const maxLines = props.maxLines ?? DEFAULT_MAX_LINES
  const cursor = props.disabled ? '' : props.cursorVisible ? '▌' : ' '
  const colors = theme.components.inputPanel
  const borderColor = props.disabled ? colors.activeBorder : colors.border
  const effectiveColumns =
    typeof columns === 'number' && Number.isFinite(columns) && columns > 0
      ? columns
      : DEFAULT_COLUMNS
  const innerWidth = Math.max(1, effectiveColumns - BORDER_WIDTH - PADDING_X * 2)
  const prefix = `${props.label} `
  const prefixWidth = stringWidth(prefix)
  const contentWidth = Math.max(1, innerWidth - prefixWidth)
  const indent = ' '.repeat(prefixWidth)

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

  const inputLines = useMemo(() => {
    if (!props.value) return []
    return wrapByWidth(`${props.value}${cursor}`, contentWidth)
  }, [contentWidth, cursor, props.value])
  const visibleInputLines = inputLines.length > maxLines ? inputLines.slice(-maxLines) : inputLines
  const contentLineCount = props.value
    ? Math.max(1, visibleInputLines.length)
    : 1
  const panelHeight = Math.min(maxLines, contentLineCount) + 2
  const placeholderLine = useMemo(() => {
    if (props.value) return ''
    const cursorWidth = stringWidth(cursor)
    const available =
      contentWidth - cursorWidth - (props.placeholder ? 1 : 0)
    const trimmedPlaceholder = props.placeholder
      ? truncateByWidth(props.placeholder, Math.max(0, available))
      : ''
    return trimmedPlaceholder
  }, [contentWidth, cursor, props.placeholder, props.value])

  return (
    <Box
      borderColor={borderColor}
      borderStyle="round"
      flexDirection="column"
      height={panelHeight}
      paddingX={1}
      paddingY={0}
      width="100%"
    >
      <Box flexDirection="column">
        {props.value ? (
          visibleInputLines.map((line, index) => (
            <Box key={`input-line-${index}`}>
              {index === 0 ? (
                <>
                  <Text color={colors.label}>{props.label}</Text>
                  <Text color={colors.text} wrap="truncate">
                    {` ${line}`}
                  </Text>
                  {props.disabled ? (
                    <Text color={colors.disabled} dimColor wrap="truncate">
                      {' (busy)'}
                    </Text>
                  ) : null}
                </>
              ) : (
                <Text color={colors.text} wrap="truncate">
                  {`${indent}${line}`}
                </Text>
              )}
            </Box>
          ))
        ) : (
          <Box>
            <Text color={colors.label}>{props.label}</Text>
            <Text color={colors.text} wrap="truncate">{` ${cursor}`}</Text>
            {props.placeholder ? (
              <Text color={colors.placeholder} dimColor wrap="truncate">
                {placeholderLine ? ` ${placeholderLine}` : ''}
              </Text>
            ) : null}
            {props.disabled ? (
              <Text color={colors.disabled} dimColor wrap="truncate">
                {' (busy)'}
              </Text>
            ) : null}
          </Box>
        )}
      </Box>
    </Box>
  )
}
