// activeTabId is the focused tab of the current window (no "tabs" permission needed for the id).
export async function activeTabId(): Promise<number | null> {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true })
  return tab?.id ?? null
}

export async function openTab(url: string): Promise<void> {
  await chrome.tabs.create({ url })
}

export async function openOptions(): Promise<void> {
  await chrome.runtime.openOptionsPage()
}
