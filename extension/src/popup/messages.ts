// connectMessage turns a ConnectResult reason into a sentence for the user.
export function connectMessage(reason: string): string {
  switch (reason) {
    case 'cancelled':
      return 'Connection cancelled.'
    case 'access_denied':
      return 'Notion access was not granted.'
    case 'network_error':
      return "Can't reach the server. Try again."
    default:
      return `Connection failed (${reason}). Try again.`
  }
}
