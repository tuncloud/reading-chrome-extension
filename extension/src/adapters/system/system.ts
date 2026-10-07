import type { Clock, Log } from '../../application/ports'

export class SystemClock implements Clock {
  now(): Date {
    return new Date()
  }

  timeZone(): string {
    return Intl.DateTimeFormat().resolvedOptions().timeZone
  }
}

// consoleLog never receives tokens or bodies: callers log ids, reasons and counts only (C13).
export function consoleLog(scope: string): Log {
  const prefix = `[reading-tracker:${scope}]`
  return {
    info: (message, ...details) => console.info(`${prefix} ${message}`, ...details),
    warn: (message, ...details) => console.warn(`${prefix} ${message}`, ...details),
  }
}
