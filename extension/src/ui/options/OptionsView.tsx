// <OptionsView> draws the options page (spec T31) for one OptionsState. It holds only
// the text typed into its forms; everything else comes from the controller.
import { useState, type FormEvent } from 'react'
import type { DomainLists } from '../../shared/domains'
import type { SessionState } from '../../application/session'
import type { NotionPage } from '../../shared/types'

export interface OptionsState {
  session: SessionState
  domains: DomainLists
  pages: NotionPage[] | null
  busy: boolean
  note: string | null
}

export interface OptionsActions {
  connect(): void
  disconnect(): void
  deleteAccount(): void
  searchPages(query: string): void
  choosePage(pageId: string): void
  enableDomain(input: string): void
  disableDomain(domain: string): void
  denyDomain(input: string): void
  undenyDomain(domain: string): void
}

interface TextFormProps {
  name: string
  placeholder: string
  label: string
  busy: boolean
  onSubmit(value: string): void
}

function TextForm({ name, placeholder, label, busy, onSubmit }: TextFormProps) {
  const [value, setValue] = useState('')
  const submit = (e: FormEvent) => {
    e.preventDefault()
    onSubmit(value) // synchronous: enableDomain's permission prompt stays in the gesture
  }
  return (
    <form data-form={name} onSubmit={submit}>
      <input type="text" placeholder={placeholder} value={value} onChange={(e) => setValue(e.target.value)} />
      <button type="submit" disabled={busy}>{label}</button>
    </form>
  )
}

function DomainList({ name, domains, busy, empty, onRemove }: {
  name: string
  domains: readonly string[]
  busy: boolean
  empty: string
  onRemove(domain: string): void
}) {
  if (domains.length === 0) return <p className="muted">{empty}</p>
  return (
    <ul data-list={name}>
      {domains.map((d) => (
        <li key={d}>
          <span>{d}</span>
          <button disabled={busy} onClick={() => onRemove(d)}>Remove</button>
        </li>
      ))}
    </ul>
  )
}

function Account({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  const { session, busy } = state
  if (session.kind === 'disconnected') {
    return (
      <section data-section="account">
        <h2>Notion</h2>
        <p>Not connected.</p>
        <button className="primary" disabled={busy} onClick={actions.connect}>Connect Notion</button>
      </section>
    )
  }
  const inst = session.installation
  const who = session.user ? ` as ${session.user.name}` : ''
  const status = !inst ? 'No workspace connected.'
    : inst.status === 'active' ? `Connected to ${inst.workspaceName}${who}.`
      : inst.status === 'revoked' ? `Access to ${inst.workspaceName} was removed.`
        : `Connected to ${inst.workspaceName}${who}; setup is not finished.`
  return (
    <section data-section="account">
      <h2>Notion</h2>
      <p>{status}</p>
      <div className="row">
        {(!inst || inst.status === 'revoked') && (
          <button className="primary" disabled={busy} onClick={actions.connect}>Reconnect</button>
        )}
        <button disabled={busy} onClick={actions.disconnect}>Disconnect</button>
        <button className="danger" disabled={busy} onClick={actions.deleteAccount}>Delete account</button>
      </div>
    </section>
  )
}

function Setup({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  const inst = state.session.kind === 'connected' ? state.session.installation : null
  if (!inst || inst.status !== 'needs_setup') return null
  return (
    <section data-section="setup">
      <h2>Finish setup</h2>
      <p>Pick a Notion page; Reading Library and Daily Log are created inside it.</p>
      {inst.setupError && <p className="muted">Reason: {inst.setupError}</p>}
      <TextForm name="search" placeholder="Search your pages" label="Search" busy={state.busy} onSubmit={actions.searchPages} />
      {state.pages !== null && (state.pages.length === 0 ? (
        <p className="muted">No pages found. Share a page with the integration in Notion, then search again.</p>
      ) : (
        <ul data-list="pages">
          {state.pages.map((p) => (
            <li key={p.id}>
              <span title={p.url}>{p.title || 'Untitled'}</span>
              <button disabled={state.busy} onClick={() => actions.choosePage(p.id)}>Use this page</button>
            </li>
          ))}
        </ul>
      ))}
    </section>
  )
}

export function OptionsView({ state, actions }: { state: OptionsState; actions: OptionsActions }) {
  return (
    <>
      <h1>Reading Tracker</h1>
      <Account state={state} actions={actions} />
      <Setup state={state} actions={actions} />
      <section data-section="enabled">
        <h2>Tracked sites</h2>
        <p className="muted">Medium and Freedium are always tracked. Add other sites to track them too.</p>
        <TextForm name="enable" placeholder="example.com" label="Track site" busy={state.busy} onSubmit={actions.enableDomain} />
        <DomainList name="enabled" domains={state.domains.enabled} busy={state.busy} empty="No other sites yet." onRemove={actions.disableDomain} />
      </section>
      <section data-section="denied">
        <h2>Never track</h2>
        <p className="muted">Sites listed here are never tracked, even if added above.</p>
        <TextForm name="deny" placeholder="example.com" label="Never track" busy={state.busy} onSubmit={actions.denyDomain} />
        <DomainList name="denied" domains={state.domains.denied} busy={state.busy} empty="Nothing blocked." onRemove={actions.undenyDomain} />
      </section>
      {state.note && <p role="status" className="note">{state.note}</p>}
    </>
  )
}
