export type Log = {
  info(message: string, ...details: unknown[]): void
  warn(message: string, ...details: unknown[]): void
}

// consoleLog never receives tokens or bodies: callers log ids, reasons and counts only (C13).
export function consoleLog(scope: string): Log {
  const prefix = `[reading-tracker:${scope}]`
  return {
    info: (message, ...details) => console.info(`${prefix} ${message}`, ...details),
    warn: (message, ...details) => console.warn(`${prefix} ${message}`, ...details),
  }
}
