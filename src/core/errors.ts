type ErrorDetails = {
  code?: string
  message?: string
}

function isNonEmptyString(value: unknown): value is string {
  return typeof value === 'string' && value.trim().length > 0
}

function formatErrorMessage(details: ErrorDetails, fallbackMessage: string): string {
  const message = details.message ?? fallbackMessage

  if (!details.code) return message
  if (message.startsWith(details.code)) return message

  return `${details.code}: ${message}`
}

function readErrorDetails(error: unknown): ErrorDetails {
  if (!error || typeof error !== 'object') return {}

  const record = error as Record<string, unknown>
  const code = isNonEmptyString(record.code) ? record.code : undefined
  const message = isNonEmptyString(record.message) ? record.message : undefined

  if (code || message) return { code, message }

  return readErrorDetails(record.error)
}

export function toError(error: unknown, fallbackMessage: string): Error {
  if (error instanceof Error) {
    const code = isNonEmptyString((error as Error & { code?: unknown }).code)
      ? String((error as Error & { code?: unknown }).code)
      : undefined
    const message = isNonEmptyString(error.message) ? error.message : fallbackMessage
    const formattedMessage = formatErrorMessage({ code, message }, fallbackMessage)

    if (error.message && formattedMessage === error.message) return error

    const wrapped = new Error(formattedMessage, { cause: error })
    wrapped.name = error.name
    return wrapped
  }

  if (typeof error === 'string') return new Error(error)

  const details = readErrorDetails(error)
  return new Error(formatErrorMessage(details, fallbackMessage))
}
