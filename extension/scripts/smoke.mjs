// End-to-end smoke test in a real browser (plan Task 13).
//
//   CHROME_BIN=<Chrome for Testing binary> pnpm smoke
//
// Needs: `pnpm build` done, the backend's local server running at VITE_API_BASE
// (backend/: make ddb-up && make run-local), and openssl on PATH.
// It never touches Notion: it checks config, alarms, the popup, the options page and local tracking only.
import { execFileSync, spawn } from 'node:child_process'
import { existsSync, mkdtempSync, readFileSync } from 'node:fs'
import { createServer } from 'node:https'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'

const root = resolve(import.meta.dirname, '..')
const dist = join(root, 'dist')
const chromeBin = process.env.CHROME_BIN
const env = Object.fromEntries(
  (existsSync(join(root, '.env.local')) ? readFileSync(join(root, '.env.local'), 'utf8') : '')
    .split('\n').filter((l) => /^\s*[A-Z_]+=/.test(l)).map((l) => l.split('=').map((x) => x.trim())),
)
const apiBase = process.env.VITE_API_BASE ?? env.VITE_API_BASE
const ARTICLE = 'https://medium.com/@jane/designing-idempotent-apis-8f3c2a1b9d4e'
const CANONICAL = 'https://medium.com/p/8f3c2a1b9d4e'
const DEBUG_PORT = 9339
const HTTPS_PORT = 8443

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))
let failures = 0
function check(ok, label, detail = '') {
  console.log(`${ok ? 'PASS' : 'FAIL'}  ${label}${detail ? `: ${detail}` : ''}`)
  if (!ok) failures++
}

function cdp(wsUrl) {
  const ws = new WebSocket(wsUrl)
  let id = 0
  const pending = new Map()
  ws.onmessage = (m) => {
    const msg = JSON.parse(m.data)
    pending.get(msg.id)?.(msg)
    pending.delete(msg.id)
  }
  const ready = new Promise((r) => { ws.onopen = r })
  const send = (method, params = {}) => new Promise((r) => {
    const i = ++id
    pending.set(i, r)
    ws.send(JSON.stringify({ id: i, method, params }))
  })
  const evaluate = async (expression) =>
    (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.result?.value
  return { ready, send, evaluate, close: () => ws.close() }
}

async function targets() {
  return (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/list`)).json()
}

const readDb = `new Promise((res) => { const r = indexedDB.open('reading-tracker'); r.onsuccess = () => {
  const tx = r.result.transaction(['segments', 'articles', 'kv']); const out = {};
  tx.objectStore('segments').getAll().onsuccess = (e) => { out.segments = e.target.result };
  tx.objectStore('articles').getAll().onsuccess = (e) => { out.articles = e.target.result };
  tx.objectStore('kv').get('config').onsuccess = (e) => { out.config = e.target.result?.value ?? null };
  tx.oncomplete = () => res(JSON.stringify(out)) } })`

async function main() {
  if (!chromeBin || !existsSync(chromeBin)) throw new Error('set CHROME_BIN to a Chrome for Testing binary')
  if (!existsSync(join(dist, 'manifest.json'))) throw new Error('run pnpm build first')
  if (!apiBase) throw new Error('VITE_API_BASE is not set (.env.local)')
  const cfg = await fetch(`${apiBase}/v1/config`).then((r) => r.json()).catch(() => null)
  if (!cfg?.tracking) throw new Error(`backend not reachable at ${apiBase}/v1/config (backend: make ddb-up && make run-local)`)

  const tmp = mkdtempSync(join(tmpdir(), 'rt-smoke-'))
  execFileSync('openssl', ['req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-days', '1', '-subj', '/CN=medium.com',
    '-addext', 'subjectAltName=DNS:medium.com', '-keyout', join(tmp, 'key.pem'), '-out', join(tmp, 'cert.pem')], { stdio: 'ignore' })
  const page = `<!doctype html><html><head><title>Designing Idempotent APIs</title><meta name="author" content="Jane Doe"></head>
    <body><article><h1>Designing Idempotent APIs</h1><p>${'word '.repeat(1500)}</p></article></body></html>`
  const server = createServer({ key: readFileSync(join(tmp, 'key.pem')), cert: readFileSync(join(tmp, 'cert.pem')) }, (req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' })
    res.end(req.url === '/elsewhere' ? '<p>elsewhere</p>' : page)
  }).listen(HTTPS_PORT)
  const chrome = spawn(chromeBin, [
    '--headless=new', `--remote-debugging-port=${DEBUG_PORT}`, `--user-data-dir=${join(tmp, 'profile')}`,
    `--disable-extensions-except=${dist}`, `--load-extension=${dist}`, '--no-first-run', '--ignore-certificate-errors',
    `--host-resolver-rules=MAP medium.com:443 127.0.0.1:${HTTPS_PORT}`, 'about:blank',
  ], { stdio: 'ignore' })

  try {
    let sw = null
    for (let i = 0; i < 60 && !sw; i++) {
      await sleep(250)
      let list = []
      try { list = await targets() } catch { continue }
      for (const t of list.filter((x) => x.type === 'service_worker')) {
        const c = cdp(t.webSocketDebuggerUrl)
        await c.ready
        if ((await c.evaluate('chrome.runtime.getManifest().name')) === 'Reading Tracker') sw = t
        c.close()
      }
    }
    check(sw !== null, 'service worker started')
    if (!sw) return
    const extId = new URL(sw.url).host
    const s = cdp(sw.webSocketDebuggerUrl)
    await s.ready
    check((await s.evaluate('chrome.runtime.getManifest().incognito')) === 'not_allowed', 'manifest incognito not_allowed')

    let alarms = ''
    for (let i = 0; i < 20 && !alarms.includes('sync'); i++) {
      await sleep(250)
      alarms = await s.evaluate('chrome.alarms.getAll().then((a) => a.map((x) => x.name).sort().join(","))')
    }
    check(alarms === 'daily,sync', 'alarms created', alarms)
    const db1 = JSON.parse(await s.evaluate(readDb))
    check(db1.config?.value?.tracking?.idleSeconds === cfg.tracking.idle_seconds, 'config cached from /v1/config')

    const popup = await (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/new?chrome-extension://${extId}/popup.html`, { method: 'PUT' })).json()
    await sleep(1500)
    const pc = cdp(popup.webSocketDebuggerUrl)
    await pc.ready
    const popupText = await pc.evaluate('document.getElementById("app").innerText')
    check(/Connect Notion/.test(popupText ?? ''), 'popup shows Connect Notion when disconnected')
    pc.close()

    const options = await (await fetch(`http://127.0.0.1:${DEBUG_PORT}/json/new?chrome-extension://${extId}/options.html`, { method: 'PUT' })).json()
    await sleep(1500)
    const oc = cdp(options.webSocketDebuggerUrl)
    await oc.ready
    const optionsText = await oc.evaluate('document.getElementById("app").innerText')
    check(/Tracked sites/.test(optionsText ?? '') && /Never track/.test(optionsText ?? ''), 'options page renders')
    oc.close()

    const tab = (await targets()).find((t) => t.type === 'page' && t.url === 'about:blank')
    const p = cdp(tab.webSocketDebuggerUrl)
    await p.ready
    await p.send('Emulation.setFocusEmulationEnabled', { enabled: true })
    await p.send('Page.enable')
    await p.send('Page.navigate', { url: ARTICLE })
    await sleep(1500)
    for (let i = 0; i < 6; i++) {
      await p.send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: 20 + i, y: 20 })
      await sleep(1000)
    }
    await p.send('Page.navigate', { url: 'https://medium.com/elsewhere' }) // pagehide flushes the segment
    await sleep(1500)
    p.close()

    const db2 = JSON.parse(await s.evaluate(readDb))
    const seg = db2.segments.find((x) => x.canonical_url === CANONICAL)
    const art = db2.articles.find((a) => a.canonical_url === CANONICAL)
    check(Boolean(seg) && seg.active_seconds >= 4 && seg.synced === 0, 'segment stored for the Medium canonical', seg ? `${seg.active_seconds}s` : 'none')
    check(art?.title === 'Designing Idempotent APIs' && art?.author === 'Jane Doe' && art?.word_count === 1503, 'article metadata extracted',
      art ? `"${art.title}" by ${art.author}, ${art.word_count} words` : 'none')
    s.close()
  } finally {
    chrome.kill()
    server.close()
  }
}

main().then(
  () => {
    console.log(failures === 0 ? '\nsmoke: all checks passed' : `\nsmoke: ${failures} check(s) failed`)
    process.exitCode = failures === 0 ? 0 : 1
  },
  (e) => {
    console.error(`smoke: ${e.message}`)
    process.exitCode = 1
  },
)
