import site from '../../site.json'

// POST /api/forms/:form stores the answers of a form (contact, booking, order...).
// Checks every value on the server: size, field count, a hidden trap field for bots,
// and a small rate limit per visitor.
//
// GET /api/forms/:form/public lists what visitors may read of a guest book form: only
// the fields "name" and "message", and only for forms the brief marks as a guest book
// (site.json). Attendance, phone, e-mail and every other field stay private.
const FORM_NAME = /^[a-z0-9][a-z0-9-]{0,39}$/
const MAX_FIELDS = 30
const MAX_VALUE = 5000
const MAX_BODY = 20000
const PER_MINUTE = 10
const PUBLIC_FIELDS = ['name', 'message']
const PUBLIC_LIMIT = 100
const PUBLIC_VALUE = 1000
const recent = new Map()

const guestbooks = new Set((site?.brief?.forms ?? []).filter((form) => form && form.guestbook === true).map((form) => String(form.name)))

function limited(visitor) {
  const now = Date.now()
  const times = (recent.get(visitor) ?? []).filter((time) => now - time < 60000)
  times.push(now)
  recent.set(visitor, times)
  if (recent.size > 5000) recent.clear()
  return times.length > PER_MINUTE
}

export function registerForms(api, db) {
  const insert = db.prepare('INSERT INTO submissions (form, data) VALUES (?, ?)')
  const latest = db.prepare('SELECT data, created_at FROM submissions WHERE form = ? ORDER BY id DESC LIMIT ?')

  api.get('/forms/:form/public', (c) => {
    const form = c.req.param('form')
    if (!FORM_NAME.test(form) || !guestbooks.has(form)) return c.json({ ok: false, error: 'Unknown form' }, 404)
    const entries = []
    for (const row of latest.all(form, PUBLIC_LIMIT)) {
      let data
      try {
        data = JSON.parse(row.data)
      } catch {
        continue
      }
      const entry = { at: row.created_at }
      for (const field of PUBLIC_FIELDS) entry[field] = String(data?.[field] ?? '').slice(0, PUBLIC_VALUE)
      if (entry.message) entries.push(entry)
    }
    c.header('cache-control', 'no-store')
    return c.json({ ok: true, entries })
  })

  api.post('/forms/:form', async (c) => {
    const form = c.req.param('form')
    if (!FORM_NAME.test(form)) return c.json({ ok: false, error: 'Unknown form' }, 404)
    const visitor = (c.req.header('x-forwarded-for') ?? 'local').split(',')[0].trim()
    if (limited(visitor)) return c.json({ ok: false, error: 'Too many messages, try again in a minute' }, 429)

    const raw = await c.req.text()
    if (raw.length > MAX_BODY) return c.json({ ok: false, error: 'Message too long' }, 413)
    let body
    try {
      body = JSON.parse(raw)
    } catch {
      return c.json({ ok: false, error: 'Invalid form data' }, 400)
    }
    if (!body || typeof body !== 'object' || Array.isArray(body)) return c.json({ ok: false, error: 'Invalid form data' }, 400)
    if (body._hp) return c.json({ ok: true })

    const data = {}
    for (const [key, value] of Object.entries(body)) {
      if (key === '_hp') continue
      if (Object.keys(data).length >= MAX_FIELDS) break
      if (!/^[a-zA-Z0-9_-]{1,40}$/.test(key)) continue
      const text = String(value ?? '').trim()
      if (text.length > MAX_VALUE) return c.json({ ok: false, error: 'A field is too long' }, 400)
      data[key] = text
    }
    if (Object.values(data).every((value) => value === '')) return c.json({ ok: false, error: 'The form is empty' }, 400)

    insert.run(form, JSON.stringify(data))
    return c.json({ ok: true })
  })
}
