// Popup composition root (spec T30): real adapters in, <PopupApp> out.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { sendToBackground, sendToTab } from '../shared/messaging'
import { activeTabId, openOptions, openTab } from '../adapters/chrome/tabs'
import { buildPopupModel } from '../application/popup-model'
import { PopupApp } from '../ui/popup/PopupApp'
import { createServices } from '../background/services'

const s = createServices('popup')

async function currentCanonical(): Promise<string | null> {
  const tabId = await activeTabId()
  return tabId === null ? null : ((await sendToTab(tabId, { type: 'current-page' }))?.canonicalUrl ?? null)
}

createRoot(document.getElementById('app') as HTMLElement).render(
  <StrictMode>
    <PopupApp
      load={async () => buildPopupModel(
        { db: s.db, api: s.api, clock: { now: () => new Date(), timeZone: () => Intl.DateTimeFormat().resolvedOptions().timeZone } },
        await s.session.state(),
        await currentCanonical(),
      )}
      refresh={async () => {
        await s.session.refresh()
      }}
      connect={() => sendToBackground({ type: 'connect' })}
      openOptions={() => void openOptions()}
      openUrl={(url) => void openTab(url)}
    />
  </StrictMode>,
)
