export interface Log {
  info(message: string, ...details: unknown[]): void
  warn(message: string, ...details: unknown[]): void
}
