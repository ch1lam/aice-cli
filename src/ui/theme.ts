const palette = {
  accents: {
    blue: '#4385BE',
    cyan: '#3AA99F',
    green: '#879A39',
    magenta: '#CE5D97',
    orange: '#DA702C',
    purple: '#8B7EC8',
    red: '#D14D41',
    yellow: '#D0A215',
  },
  accentsLight: {
    blue: '#205EA6',
    cyan: '#24837B',
    green: '#66800B',
    magenta: '#A02F6F',
    orange: '#BC5215',
    purple: '#5E409D',
    red: '#AF3029',
    yellow: '#AD8301',
  },
  base: {
    border: '#343331',
    borderMuted: '#575653',
    surface: '#100F0F',
    surfaceActive: '#282726',
    surfaceRaised: '#1C1B1A',
    textDisabled: '#878580',
    textMuted: '#CECDC3',
    textPrimary: '#FFFCF0',
    textSecondary: '#E6E4D9',
  },
} as const

const semantic = {
  accentCode: palette.accents.orange,
  accentDanger: palette.accents.red,
  accentInfo: palette.accents.blue,
  accentPrimary: palette.accents.cyan,
  accentSecondary: palette.accents.yellow,
  accentSuccess: palette.accents.green,
  accentUser: palette.accents.magenta,
  accentWarning: palette.accents.yellow,
  active: palette.base.surfaceActive,
  background: palette.base.surface,
  border: palette.base.border,
  borderMuted: palette.base.borderMuted,
  panel: palette.base.surfaceRaised,
  textDisabled: palette.base.textDisabled,
  textMuted: palette.base.textMuted,
  textPrimary: palette.base.textPrimary,
  textSecondary: palette.base.textSecondary,
} as const

const components = {
  app: {
    title: semantic.accentPrimary,
  },
  chatWindow: {
    assistantLabel: semantic.accentSuccess,
    content: semantic.textPrimary,
    userLabel: semantic.accentUser,
  },
  inputPanel: {
    activeBorder: semantic.accentSecondary,
    border: semantic.accentSuccess,
    disabled: semantic.textDisabled,
    label: semantic.textPrimary,
    placeholder: semantic.textMuted,
    text: semantic.textPrimary,
  },
  messages: {
    assistant: semantic.textPrimary,
    caret: semantic.accentPrimary,
    placeholder: semantic.textMuted,
    system: semantic.accentSuccess,
    text: semantic.textPrimary,
    user: semantic.textDisabled,
  },
  selectInput: {
    activeBorder: semantic.accentSecondary,
    border: semantic.border,
    helper: semantic.textMuted,
    label: semantic.textPrimary,
    selected: semantic.accentSecondary,
    title: semantic.accentPrimary,
    value: semantic.textMuted,
  },
  slashSuggestions: {
    border: semantic.borderMuted,
    command: semantic.accentSecondary,
    description: semantic.textMuted,
    helper: semantic.textMuted,
    hint: semantic.textSecondary,
    selected: semantic.accentPrimary,
  },
  statusBar: {
    error: semantic.accentDanger,
    provider: semantic.accentSecondary,
    separator: semantic.textMuted,
    status: semantic.accentPrimary,
    usage: semantic.accentInfo,
  },
} as const

export const flexokiDarkTheme = {
  components,
  name: 'flexoki-dark',
  palette,
  semantic,
} as const

export type Theme = typeof flexokiDarkTheme

export const theme: Theme = flexokiDarkTheme
