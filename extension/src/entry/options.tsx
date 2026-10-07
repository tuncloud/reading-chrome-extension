// Options page composition root (spec T31). Connect goes through the background (like the
// popup) so the outbox sync after connecting runs in the service worker.
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { sendToBackground } from '../shared/messaging'
import { OptionsApp } from '../ui/options/OptionsApp'
import { compose } from './compose'

const s = compose('options')

createRoot(document.getElementById('app') as HTMLElement).render(
  <StrictMode>
    <OptionsApp
      services={{ session: s.session, setup: s.setup, domains: s.domains, connect: () => sendToBackground({ type: 'connect' }) }}
      confirm={(message) => window.confirm(message)}
    />
  </StrictMode>,
)
