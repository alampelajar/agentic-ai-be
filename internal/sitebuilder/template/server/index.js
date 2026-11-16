// The site's server: the built pages, and the site's own API under /api.
// Fixed by the template; the agent adds API routes in server/routes.
import { Hono } from 'hono'
import { serve } from '@hono/node-server'
import { readFile, stat } from 'node:fs/promises'
import { extname, join, normalize, sep } from 'node:path'
import { openDatabase } from './db.js'
import { registerRoutes } from './routes/index.js'

const PUBLIC_DIR = join(process.cwd(), 'public')
const TYPES = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp',
  '.gif': 'image/gif', '.ico': 'image/x-icon', '.woff2': 'font/woff2', '.woff': 'font/woff', '.json': 'application/json',
  '.txt': 'text/plain; charset=utf-8',
}

const db = openDatabase()
const app = new Hono()

app.get('/api/health', (c) => c.json({ ok: true }))
const api = new Hono()
registerRoutes(api, db)
app.route('/api', api)
app.all('/api/*', (c) => c.json({ ok: false, error: 'Not found' }, 404))

async function file(path) {
  const target = normalize(join(PUBLIC_DIR, decodeURIComponent(path)))
  if (target !== PUBLIC_DIR && !target.startsWith(PUBLIC_DIR + sep)) return null
  try {
    const info = await stat(target)
    return info.isFile() ? target : null
  } catch {
    return null
  }
}

app.get('*', async (c) => {
  const path = new URL(c.req.url).pathname
  const found = path === '/' ? null : await file(path)
  const target = found ?? join(PUBLIC_DIR, 'index.html')
  const headers = {
    'content-type': TYPES[extname(target)] ?? 'application/octet-stream',
    'cache-control': found && path.startsWith('/assets/') ? 'public, max-age=31536000, immutable' : 'no-cache',
    'x-content-type-options': 'nosniff',
  }
  return c.body(await readFile(target), 200, headers)
})

const port = Number(process.env.PORT || 3000)
serve({ fetch: app.fetch, port, hostname: process.env.HOST || '0.0.0.0' }, () => console.log(`site listening on ${port}`))
