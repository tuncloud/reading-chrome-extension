// isHttpsUrl: server-supplied links are opened only when they are https (never javascript:, file: ...).
export function isHttpsUrl(url: string): boolean {
  try {
    return new URL(url).protocol === 'https:'
  } catch {
    return false
  }
}
