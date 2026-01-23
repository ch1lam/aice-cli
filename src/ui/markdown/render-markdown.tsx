import type { ReactNode } from 'react'

import { Text } from 'ink'
import remend from 'remend'
import stringWidth from 'string-width'

import { renderInline } from './render-inline.js'

export type ParsedLineKind = 'blank' | 'code' | 'heading' | 'hr' | 'list' | 'quote' | 'text'

export type ParsedLine = {
  kind: ParsedLineKind
  level?: number
  marker?: string
  ordered?: boolean
  text: string
}

export type RenderedLine = {
  bodyPrefix: string
  color: string
  parts: ReactNode[]
}

export type MarkdownRenderOptions = {
  baseColor: string
  codeColor: string
  contentWidth: number
  headingColor: string
  linkColor: string
  mutedColor: string
}

type WrappedSegment = { prefix: string; text: string }

type FenceState = {
  active: boolean
  marker: string
}

const HR_REGEX = /^\s*([-*_])\1\1+\s*$/
const HEADING_REGEX = /^\s*(#{1,6})\s+(.*)$/
const ORDERED_LIST_REGEX = /^\s*(\d+)\.\s+(.*)$/
const UNORDERED_LIST_REGEX = /^\s*[-*+•·]\s+(.*)$/
const QUOTE_REGEX = /^\s*>\s?(.*)$/
const FENCE_REGEX = /^\s*(```|~~~)/

function normalizeLine(rawLine: string): string {
  return rawLine.replace(/^(?:\u200B|\u200C|\u200D|\uFEFF)+/, '')
}

function parseMarkdownLines(text: string): ParsedLine[] {
  const normalized = text.replaceAll('\r\n', '\n')
  const rawLines = normalized.split('\n')
  const lines: ParsedLine[] = []
  const fence: FenceState = { active: false, marker: '' }

  for (const rawLine of rawLines) {
    const fenceMatch = rawLine.match(FENCE_REGEX)
    if (fenceMatch) {
      if (fence.active && fenceMatch[1] === fence.marker) {
        fence.active = false
        fence.marker = ''
      } else if (!fence.active) {
        fence.active = true
        fence.marker = fenceMatch[1]
      }

      continue
    }

    if (fence.active) {
      lines.push({ kind: 'code', text: rawLine })
      continue
    }

    const line = normalizeLine(rawLine)

    if (line.trim() === '') {
      lines.push({ kind: 'blank', text: '' })
      continue
    }

    if (HR_REGEX.test(line)) {
      lines.push({ kind: 'hr', text: '' })
      continue
    }

    const headingMatch = line.match(HEADING_REGEX)
    if (headingMatch) {
      lines.push({ kind: 'heading', level: headingMatch[1].length, text: headingMatch[2] })
      continue
    }

    const orderedMatch = line.match(ORDERED_LIST_REGEX)
    if (orderedMatch) {
      lines.push({ kind: 'list', marker: orderedMatch[1], ordered: true, text: orderedMatch[2] })
      continue
    }

    const unorderedMatch = line.match(UNORDERED_LIST_REGEX)
    if (unorderedMatch) {
      lines.push({ kind: 'list', ordered: false, text: unorderedMatch[1] })
      continue
    }

    const quoteMatch = line.match(QUOTE_REGEX)
    if (quoteMatch) {
      lines.push({ kind: 'quote', text: quoteMatch[1] })
      continue
    }

    lines.push({ kind: 'text', text: line })
  }

  return lines
}

function compactBlankLines(lines: ParsedLine[]): ParsedLine[] {
  const result: ParsedLine[] = []
  let lastWasBlank = false

  for (const line of lines) {
    if (line.kind === 'blank') {
      if (lastWasBlank) continue
      lastWasBlank = true
      result.push(line)
      continue
    }

    lastWasBlank = false
    result.push(line)
  }

  while (result.length > 0 && result.at(-1)?.kind === 'blank') {
    result.pop()
  }

  return result
}

function splitByWidth(text: string, maxWidth: number): { line: string; rest: string } {
  if (maxWidth <= 0 || text === '') {
    return { line: '', rest: text }
  }

  let width = 0
  let offset = 0
  for (const char of text) {
    const charWidth = stringWidth(char)
    if (width + charWidth > maxWidth) break
    width += charWidth
    offset += char.length
  }

  return { line: text.slice(0, offset), rest: text.slice(offset) }
}

function wrapWithPrefix(
  text: string,
  maxWidth: number,
  firstPrefix: string,
  nextPrefix: string,
): WrappedSegment[] {
  if (text === '') {
    return [{ prefix: firstPrefix, text: '' }]
  }

  const segments: WrappedSegment[] = []
  let remaining = text
  let first = true

  while (remaining.length > 0) {
    const prefix = first ? firstPrefix : nextPrefix
    const available = Math.max(0, maxWidth - stringWidth(prefix))
    const { line, rest } = splitByWidth(remaining, available)
    segments.push({ prefix, text: line })
    remaining = rest
    first = false
    if (available === 0) break
  }

  return segments.length > 0 ? segments : [{ prefix: firstPrefix, text: '' }]
}

function lineColor(kind: ParsedLineKind, options: MarkdownRenderOptions): string {
  if (kind === 'code') return options.codeColor
  if (kind === 'heading') return options.headingColor
  if (kind === 'hr') return options.mutedColor
  if (kind === 'quote') return options.mutedColor
  return options.baseColor
}

function buildSegments(
  line: ParsedLine,
  text: string,
  options: MarkdownRenderOptions,
  keyPrefix: string,
): ReactNode[] {
  if (line.kind === 'code') {
    return [
      <Text color={options.codeColor} key={`${keyPrefix}-code`}>
        {text}
      </Text>,
    ]
  }

  if (line.kind === 'heading') {
    return [
      <Text bold color={options.headingColor} key={`${keyPrefix}-heading`}>
        {text}
      </Text>,
    ]
  }

  if (line.kind === 'hr') {
    return [
      <Text color={options.mutedColor} key={`${keyPrefix}-hr`}>
        {text}
      </Text>,
    ]
  }

  return renderInline(
    text,
    { base: lineColor(line.kind, options), code: options.codeColor, link: options.linkColor },
    keyPrefix,
  )
}

function renderParsedLine(
  line: ParsedLine,
  options: MarkdownRenderOptions,
  lineKey: string,
): RenderedLine[] {
  if (line.kind === 'blank') {
    return [{ bodyPrefix: '', color: options.baseColor, parts: [] }]
  }

  if (line.kind === 'hr') {
    const rule = '-'.repeat(Math.max(1, options.contentWidth))
    return [
      {
        bodyPrefix: '',
        color: options.mutedColor,
        parts: buildSegments(line, rule, options, lineKey),
      },
    ]
  }

  let firstPrefix = ''
  let nextPrefix = ''
  const { text } = line

  if (line.kind === 'quote') {
    firstPrefix = '| '
    nextPrefix = '| '
  } else if (line.kind === 'list') {
    const bullet = line.ordered ? `${line.marker ?? '1'}. ` : '• '
    firstPrefix = bullet
    nextPrefix = ' '.repeat(stringWidth(bullet))
  }

  const wrapped = wrapWithPrefix(text, options.contentWidth, firstPrefix, nextPrefix)
  return wrapped.map((segment, index) => ({
    bodyPrefix: segment.prefix,
    color: lineColor(line.kind, options),
    parts: buildSegments(line, segment.text, options, `${lineKey}-${index}`),
  }))
}

export function renderMarkdownLines(text: string, options: MarkdownRenderOptions): RenderedLine[] {
  if (text === '') {
    return [{ bodyPrefix: '', color: options.baseColor, parts: [] }]
  }

  const parsed = compactBlankLines(parseMarkdownLines(text))
  const rendered: RenderedLine[] = []

  for (const [index, line] of parsed.entries()) {
    rendered.push(...renderParsedLine(line, options, `line-${index}`))
  }

  return rendered.length > 0 ? rendered : [{ bodyPrefix: '', color: options.baseColor, parts: [] }]
}

export function renderDisplayMarkdownLines(
  text: string,
  options: MarkdownRenderOptions,
): RenderedLine[] {
  return renderMarkdownLines(remend(text), options)
}

export function renderStreamMarkdown(
  rawText: string,
  options: MarkdownRenderOptions,
): { committed: RenderedLine[]; live: null | RenderedLine } {
  const displayText = remend(rawText)
  const parsed = compactBlankLines(parseMarkdownLines(displayText))
  const endsWithNewline = rawText.endsWith('\n')

  if (parsed.length === 0) {
    return { committed: [], live: endsWithNewline ? null : { bodyPrefix: '', color: options.baseColor, parts: [] } }
  }

  const committed: RenderedLine[] = []
  let live: null | RenderedLine = null

  for (const [index, line] of parsed.entries()) {
    const rendered = renderParsedLine(line, options, `stream-${index}`)
    const isLast = index === parsed.length - 1

    if (isLast && !endsWithNewline) {
      if (rendered.length > 1) {
        committed.push(...rendered.slice(0, -1))
      }

      live = rendered.at(-1) ?? null
      continue
    }

    committed.push(...rendered)
  }

  return { committed, live }
}
