// <OptionsApp> connects the controller to React: the controller's render callback sets
// state, and React draws <OptionsView>. It loads, refreshes /v1/me, then reloads.
import { useEffect, useState } from 'react'
import { createOptionsController, type OptionsServices } from './options-controller'
import { OptionsView, type OptionsActions, type OptionsState } from './OptionsView'

export interface OptionsAppProps {
  services: OptionsServices
  confirm(message: string): boolean
}

export function OptionsApp({ services, confirm }: OptionsAppProps) {
  const [view, setView] = useState<{ state: OptionsState; actions: OptionsActions } | null>(null)

  useEffect(() => {
    let live = true
    const controller = createOptionsController(services, {
      render: (state, actions) => {
        if (live) setView({ state, actions })
      },
      confirm,
    })
    void controller
      .load()
      .then(() => services.session.refresh())
      .then(() => controller.load())
    return () => {
      live = false
    }
    // One controller per page load; services and confirm are fixed for the page's lifetime.
  }, [])

  if (!view) return <p className="muted">Loading…</p>
  return <OptionsView state={view.state} actions={view.actions} />
}
