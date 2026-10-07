// ApiError is a non-2xx answer with the §2.0 error body; NetworkError is no answer at all.

export class ApiError extends Error {
  override name = 'ApiError'
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

export class NetworkError extends Error {
  override name = 'NetworkError'
}

export function isApiError(e: unknown, status?: number, code?: string): e is ApiError {
  return e instanceof ApiError && (status === undefined || e.status === status) && (code === undefined || e.code === code)
}
