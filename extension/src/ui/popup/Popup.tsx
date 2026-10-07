// <Popup> draws one PopupModel (spec T30). It holds no state: PopupApp owns loading.
import type { ArticleView, PopupModel } from '../../application/popup-model'

export interface PopupActions {
  connect(): void
  openOptions(): void
  openUrl(url: string): void
}

function articleLine(a: ArticleView): string {
  return `${a.minutes} min · ${a.progressPercent}%${a.completed ? ' · ✓' : ''}`
}

function Body({ model, actions }: { model: PopupModel; actions: PopupActions }) {
  switch (model.kind) {
    case 'disconnected':
      return (
        <>
          <p>Track your reading time and log it to Notion.</p>
          <button className="primary" onClick={actions.connect}>Connect Notion</button>
        </>
      )
    case 'needs_setup':
      return (
        <>
          <p>Almost there: {model.workspaceName} still needs its Reading Library.</p>
          {model.setupError && <p className="muted">Reason: {model.setupError}</p>}
          <button className="primary" onClick={actions.openOptions}>Finish setup</button>
        </>
      )
    case 'revoked':
      return (
        <>
          <p>Notion access for {model.workspaceName} was removed. Your reading is kept and syncs after you reconnect.</p>
          <button className="primary" onClick={actions.connect}>Reconnect</button>
        </>
      )
    case 'active': {
      const library = model.libraryUrl
      return (
        <>
          <section className="today">
            <span className="muted">Today</span>
            <strong data-role="today">{model.todayMinutes} min</strong>
          </section>
          {model.current && (
            <section data-role="current">
              <h2>Reading now</h2>
              <p className="title">{model.current.title}</p>
              <p className="muted">{articleLine(model.current)}</p>
            </section>
          )}
          {model.recent.length === 0 ? (
            <p className="muted">Nothing read yet. Open an article to start.</p>
          ) : (
            <section>
              <h2>Recent</h2>
              <ul data-role="recent">
                {model.recent.map((a) => (
                  <li key={a.url}>
                    <button className="link" title={a.url} onClick={() => actions.openUrl(a.notionUrl ?? a.url)}>{a.title}</button>
                    <span className="muted">{articleLine(a)}</span>
                  </li>
                ))}
              </ul>
            </section>
          )}
          {library && <button onClick={() => actions.openUrl(library)}>Open library in Notion</button>}
          {model.unsynced > 0 && <p className="muted small">{model.unsynced} segments waiting to sync</p>}
        </>
      )
    }
  }
}

export function Popup({ model, actions, note }: { model: PopupModel; actions: PopupActions; note?: string }) {
  return (
    <>
      <header>
        <h1>Reading Tracker</h1>
      </header>
      <Body model={model} actions={actions} />
      {note && <p role="status" className="note">{note}</p>}
    </>
  )
}
