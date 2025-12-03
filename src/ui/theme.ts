const palette = {
  accents: {
    blue: '#4385BE',
    cyan: '#3AA99F',
    green: '#879A39',
    magenta: '#CE5D97',
    orange: '#BC5215',
    red: '#D14D41',
    yellow: '#D0A215',
  },
  base: {
    border: '#343331',
    borderMuted: '#575653',
    surface: '#100F0F',
    surfaceActive: '#282726',
    surfaceRaised: '#1C1B1A',
    textDisabled: '#6F6E69',
    textMuted: '#878580',
    textPrimary: '#CECDC3',
    textSecondary: '#B7B5AC',
  },
} as const

const semantic = {
  accentCode: palette.accents.orange,
  accentDanger: palette.accents.red,
  accentInfo: palette.accents.blue,
  accentPrimary: palette.accents.yellow,
  accentSecondary: palette.accents.cyan,
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
    title: semantic.accentSecondary,
  },
  chatWindow: {
    assistantLabel: semantic.accentSuccess,
    content: semantic.textPrimary,
    userLabel: semantic.accentUser,
  },
  inputPanel: {
    activeBorder: semantic.accentPrimary,
    border: semantic.accentSecondary,
    disabled: semantic.textDisabled,
    label: semantic.accentPrimary,
    placeholder: semantic.textMuted,
    text: semantic.textPrimary,
  },
  messages: {
    assistant: semantic.accentSuccess,
    caret: semantic.accentPrimary,
    placeholder: semantic.textMuted,
    system: semantic.textMuted,
    text: semantic.textPrimary,
    user: semantic.accentUser,
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
