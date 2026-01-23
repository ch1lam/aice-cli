import type { ReactNode } from 'react'

import { Text } from 'ink'

export type InlineColors = {
  base: string
  code: string
  link: string
}

const BOLD_MARKER_LENGTH = 2
const ITALIC_MARKER_LENGTH = 1
const STRIKETHROUGH_MARKER_LENGTH = 2
const INLINE_CODE_MARKER_LENGTH = 1
const UNDERLINE_TAG_START_LENGTH = 3
const UNDERLINE_TAG_END_LENGTH = 4

const INLINE_REGEX =
  /(\*\*.*?\*\*|\*.*?\*|_.*?_|~~.*?~~|\[.*?\]\(.*?\)|`+.+?`+|<u>.*?<\/u>|https?:\/\/\S+)/g

function isWordBoundary(char?: string): boolean {
  return !char || !/\w/.test(char)
}

type MatchContext = {
  colors: InlineColors
  fullMatch: string
  key: string
  matchIndex: number
  text: string
}

function renderBoldMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (
    fullMatch.startsWith('**') &&
    fullMatch.endsWith('**') &&
    fullMatch.length > BOLD_MARKER_LENGTH * 2
  ) {
    return (
      <Text bold color={colors.base} key={key}>
        {fullMatch.slice(BOLD_MARKER_LENGTH, -BOLD_MARKER_LENGTH)}
      </Text>
    )
  }

  return null
}

function renderItalicMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key, matchIndex, text } = context
  if (
    fullMatch.length > ITALIC_MARKER_LENGTH * 2 &&
    ((fullMatch.startsWith('*') && fullMatch.endsWith('*')) ||
      (fullMatch.startsWith('_') && fullMatch.endsWith('_')))
  ) {
    const prev = text[matchIndex - 1]
    const next = text[matchIndex + fullMatch.length]
    if (isWordBoundary(prev) && isWordBoundary(next)) {
      return (
        <Text color={colors.base} italic key={key}>
          {fullMatch.slice(ITALIC_MARKER_LENGTH, -ITALIC_MARKER_LENGTH)}
        </Text>
      )
    }
  }

  return null
}

function renderStrikeMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (
    fullMatch.startsWith('~~') &&
    fullMatch.endsWith('~~') &&
    fullMatch.length > STRIKETHROUGH_MARKER_LENGTH * 2
  ) {
    return (
      <Text color={colors.base} key={key} strikethrough>
        {fullMatch.slice(STRIKETHROUGH_MARKER_LENGTH, -STRIKETHROUGH_MARKER_LENGTH)}
      </Text>
    )
  }

  return null
}

function renderCodeMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (
    fullMatch.startsWith('`') &&
    fullMatch.endsWith('`') &&
    fullMatch.length > INLINE_CODE_MARKER_LENGTH
  ) {
    const codeMatch = fullMatch.match(/^(`+)(.+?)\1$/s)
    if (codeMatch && codeMatch[2]) {
      return (
        <Text color={colors.code} key={key}>
          {codeMatch[2]}
        </Text>
      )
    }
  }

  return null
}

function renderLinkMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (fullMatch.startsWith('[') && fullMatch.includes('](') && fullMatch.endsWith(')')) {
    const linkMatch = fullMatch.match(/\[(.*?)\]\((.*?)\)/)
    if (linkMatch) {
      return (
        <Text color={colors.base} key={key}>
          {linkMatch[1]}
          <Text color={colors.link}> ({linkMatch[2]})</Text>
        </Text>
      )
    }
  }

  return null
}

function renderUnderlineMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (
    fullMatch.startsWith('<u>') &&
    fullMatch.endsWith('</u>') &&
    fullMatch.length > UNDERLINE_TAG_START_LENGTH + UNDERLINE_TAG_END_LENGTH
  ) {
    return (
      <Text color={colors.base} key={key} underline>
        {fullMatch.slice(UNDERLINE_TAG_START_LENGTH, -UNDERLINE_TAG_END_LENGTH)}
      </Text>
    )
  }

  return null
}

function renderUrlMatch(context: MatchContext): null | ReactNode {
  const { colors, fullMatch, key } = context
  if (fullMatch.startsWith('http://') || fullMatch.startsWith('https://')) {
    return (
      <Text color={colors.link} key={key}>
        {fullMatch}
      </Text>
    )
  }

  return null
}

const MATCH_RENDERERS = [
  renderBoldMatch,
  renderItalicMatch,
  renderStrikeMatch,
  renderCodeMatch,
  renderLinkMatch,
  renderUnderlineMatch,
  renderUrlMatch,
]

function renderMatch(context: MatchContext): null | ReactNode {
  for (const renderer of MATCH_RENDERERS) {
    const rendered = renderer(context)
    if (rendered) return rendered
  }

  return null
}

export function renderInline(text: string, colors: InlineColors, keyPrefix = 'i'): ReactNode[] {
  if (!/[*_~`<]|https?:\/\//.test(text)) {
    return [text]
  }

  const nodes: ReactNode[] = []
  let lastIndex = 0
  INLINE_REGEX.lastIndex = 0
  let match
  let index = 0

  while ((match = INLINE_REGEX.exec(text)) !== null) {
    if (match.index > lastIndex) {
      nodes.push(
        <Text color={colors.base} key={`${keyPrefix}-t-${index}`}>
          {text.slice(lastIndex, match.index)}
        </Text>,
      )
      index += 1
    }

    const fullMatch = match[0]
    const key = `${keyPrefix}-m-${index}`
    let rendered: null | ReactNode = null

    try {
      rendered = renderMatch({
        colors,
        fullMatch,
        key,
        matchIndex: match.index,
        text,
      })
    } catch {
      rendered = null
    }

    nodes.push(
      rendered ?? (
        <Text color={colors.base} key={key}>
          {fullMatch}
        </Text>
      ),
    )

    lastIndex = INLINE_REGEX.lastIndex
    index += 1
  }

  if (lastIndex < text.length) {
    nodes.push(
      <Text color={colors.base} key={`${keyPrefix}-t-${index}`}>
        {text.slice(lastIndex)}
      </Text>,
    )
  }

  return nodes
}
