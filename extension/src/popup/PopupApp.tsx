// <PopupApp> loads the popup model, refreshes /v1/me in the background, and handles connect.
// Connect itself runs in the service worker: the popup closes when the Notion window opens.
import { useEffect, useState } from 'react'
import type { PopupModel } from './model'
import type { ConnectResult } from '../background/session'
import { connectMessage } from './messages'
import { Popup, type PopupActions } from './Popup'

export interface PopupAppProps {
  load(): Promise<PopupModel>
  refresh(): Promise<void>
  connect(): Promise<ConnectResult>
  openOptions(): void
  openUrl(url: string): void
}

const LOAD_FAILED = "Couldn't load your reading. Close and reopen the popup."

export function PopupApp(props: PopupAppProps) {
  const [model, setModel] = useState<PopupModel | null>(null)
  const [note, setNote] = useState<string | undefined>()

  useEffect(() => {
    let live = true
    const show = async () => {
      const m = await props.load()
      if (live) setModel(m)
    }
    show()
      .then(() => props.refresh())
      .then(show)
      .catch(() => {
        if (live) setNote(LOAD_FAILED)
      })
    return () => {
      live = false
    }
    // Load once per popup opening; the props are fixed for the popup's lifetime.
  }, [])

  const actions: PopupActions = {
    connect: () => {
      props.connect().then(
        async (res) => {
          setNote(res.ok ? undefined : connectMessage(res.reason))
          setModel(await props.load())
        },
        () => setNote(connectMessage('network_error')),
      )
    },
    openOptions: props.openOptions,
    openUrl: props.openUrl,
  }

  if (!model) return note ? <p role="status" className="note">{note}</p> : <p className="muted">Loading…</p>
  return <Popup model={model} actions={actions} note={note} />
}
