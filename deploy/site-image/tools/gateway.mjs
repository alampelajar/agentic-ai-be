// The gateway in front of every running Website Builder site.
//
//   p-<site id>-<expiry>-<signature>.<PREVIEW_DOMAIN>   a preview, only with a valid signed name
//   <slug>.<SITES_DOMAIN>                               a published site, public
//   contoh-<look>.<PREVIEW_DOMAIN>                      the example website of a look, public,
//                                                       static files from /samples/<look>;
//                                                       ?warna=<variant> shows it in that palette
//
// It runs from the site image, started by cmd/site-runner, which also writes
// /config/routes.json ({"live": {"<slug>": <site id>}}). It forwards a request to the
// site's container by name on that site's own internal network; it never runs site code.
import { createServer, request as forward } from 'node:http'
import { createHmac, timingSafeEqual } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import { extname, join, normalize, sep } from 'node:path'

const PORT = Number(process.env.PORT || 8080)
const PREVIEW_DOMAIN = (process.env.PREVIEW_DOMAIN || 'preview.localhost').toLowerCase()
const SITES_DOMAIN = (process.env.SITES_DOMAIN || 'sites.localhost').toLowerCase()
const SECRET = process.env.PREVIEW_SECRET || ''
const ROUTES_FILE = '/config/routes.json'
if (SECRET.length < 32) {
  console.error('PREVIEW_SECRET must be at least 32 characters.')
  process.exit(1)
}

let routes = { live: {} }
let routesStamp = 0
function liveRoutes() {
  try {
    const stamp = statSync(ROUTES_FILE).mtimeMs
    if (stamp !== routesStamp) {
      routes = JSON.parse(readFileSync(ROUTES_FILE, 'utf8'))
      routesStamp = stamp
    }
  } catch {}
  return routes.live || {}
}

const PREVIEW_LABEL = /^p-([0-9]{1,10})-([0-9a-z]{1,10})-([0-9a-f]{24})$/
const SAMPLE_LABEL = /^contoh-([a-z0-9-]{2,40})$/
const SAMPLES = '/samples'
const VARIANT = /^[a-z][a-z-]{1,29}$/

// variantStyle is a <style> that gives the page another of its look's palettes: the
// pages only use the theme's colour variables, so replacing them recolours everything.
function variantStyle(key, variant) {
  if (!VARIANT.test(variant ?? '')) return ''
  try {
    const palette = JSON.parse(readFileSync(join(SAMPLES, key, 'variants.json'), 'utf8'))[variant]
    if (!palette) return ''
    const rules = Object.entries(palette)
      .filter(([name, value]) => /^[a-z_]+$/.test(name) && /^#[0-9A-Fa-f]{6}$/.test(value))
      .map(([name, value]) => `--color-${name.replaceAll('_', '-')}:${value};`)
    return `<style>:root{${rules.join('')}}</style>`
  } catch {
    return ''
  }
}
const TYPES = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml', '.png': 'image/png', '.jpg': 'image/jpeg', '.jpeg': 'image/jpeg', '.webp': 'image/webp',
  '.woff2': 'font/woff2', '.woff': 'font/woff', '.ico': 'image/x-icon', '.json': 'application/json',
}

// sample serves an example website: its files, and index.html for every page path.
function sample(key, url, response) {
  const root = join(SAMPLES, key)
  const address = new URL(url, 'http://x')
  const path = decodeURIComponent(address.pathname)
  let file = normalize(join(root, path))
  if (file !== root && !file.startsWith(root + sep)) file = join(root, 'index.html')
  try {
    if (!statSync(file).isFile()) file = join(root, 'index.html')
  } catch {
    file = join(root, 'index.html')
  }
  try {
    let body = readFileSync(file)
    const style = extname(file) === '.html' ? variantStyle(key, address.searchParams.get('warna')) : ''
    if (style) body = Buffer.from(body.toString('utf8').replace('</head>', style + '</head>'))
    response.writeHead(200, { 'content-type': TYPES[extname(file)] ?? 'application/octet-stream', 'cache-control': 'public, max-age=300', 'x-content-type-options': 'nosniff' })
    response.end(body)
  } catch {
    page(response, 404, 'sampleMissing', false, response.req?.headers['accept-language'])
  }
}
const SLUG_LABEL = /^[a-z0-9](?:[a-z0-9-]{1,38}[a-z0-9])$/

function previewTarget(label) {
  const match = PREVIEW_LABEL.exec(label)
  if (!match) return null
  const [, id, expiry, signature] = match
  if (parseInt(expiry, 36) * 1000 < Date.now()) return { expired: true }
  const expected = createHmac('sha256', SECRET).update(`preview:${id}:${expiry}`).digest('hex').slice(0, 24)
  if (!timingSafeEqual(Buffer.from(expected), Buffer.from(signature))) return null
  return { container: `agentic-site-p-${id}` }
}

// Previews, examples and published sites may share one domain (a free certificate
// covers one level of names only). Then a name is a preview when it has the preview
// shape (p-...), an example when it starts with contoh-, and otherwise a published
// site; published names can never take those shapes (CheckSlug in the backend).
function target(hostHeader) {
  const host = String(hostHeader || '').toLowerCase().replace(/:\d+$/, '')
  if (host.endsWith('.' + PREVIEW_DOMAIN)) {
    const label = host.slice(0, -(PREVIEW_DOMAIN.length + 1))
    if (label.includes('.')) return null
    const example = SAMPLE_LABEL.exec(label)
    if (example) return { sample: example[1] }
    if (PREVIEW_DOMAIN !== SITES_DOMAIN || label.startsWith('p-')) return { preview: true, ...previewTarget(label) }
  }
  if (host.endsWith('.' + SITES_DOMAIN)) {
    const slug = host.slice(0, -(SITES_DOMAIN.length + 1))
    if (!SLUG_LABEL.test(slug)) return null
    const id = liveRoutes()[slug]
    return id ? { container: `agentic-site-l-${id}` } : null
  }
  return null
}

// The gateway's own pages, in Indonesian and English. It cannot know the builder's
// language, so it shows both: first the one the visitor's browser asks for.
const NOTICES = {
  missing: {
    id: ['Website tidak ditemukan', 'Tidak ada website di alamat ini.'],
    en: ['Not found', 'There is no website at this address.'],
  },
  sampleMissing: {
    id: ['Contoh tidak ditemukan', 'Contoh ini belum tersedia.'],
    en: ['Not found', 'This example is not available.'],
  },
  expired: {
    id: ['Tautan sudah kedaluwarsa', 'Buka lagi website Anda dari pembuat website.'],
    en: ['Preview link expired', 'Open the preview again from the builder.'],
  },
  starting: {
    id: ['Menyiapkan website Anda...', 'Ini butuh beberapa detik. Halaman ini muncul sendiri setelah siap.'],
    en: ['Starting the preview...', 'This takes a few seconds. The page reloads by itself.'],
  },
  unavailable: {
    id: ['Sebentar lagi kembali', 'Website ini sedang tidak bisa dibuka dan akan segera kembali.'],
    en: ['Temporarily unavailable', 'This website will be back shortly.'],
  },
}

// prefersIndonesian reads the Accept-Language header: Indonesian (or Malay) before English.
export function prefersIndonesian(acceptLanguage) {
  for (const part of String(acceptLanguage || '').toLowerCase().split(',')) {
    const tag = part.split(';')[0].trim()
    if (tag.startsWith('id') || tag.startsWith('ms')) return true
    if (tag.startsWith('en')) return false
  }
  return false
}

function page(response, status, notice, refresh, acceptLanguage) {
  const [first, second] = prefersIndonesian(acceptLanguage) ? ['id', 'en'] : ['en', 'id']
  const [title, text] = NOTICES[notice][first]
  const [otherTitle, otherText] = NOTICES[notice][second]
  response.writeHead(status, { 'content-type': 'text/html; charset=utf-8', 'cache-control': 'no-store' })
  response.end(`<!doctype html><html lang="${first}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">${refresh ? '<meta http-equiv="refresh" content="3">' : ''}<title>${title}</title><style>body{font-family:system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#333;background:#fafafa}main{max-width:28rem;padding:2rem;text-align:center}.other{margin-top:2rem;color:#737373;font-size:.875rem}</style></head><body><main><h1 style="font-size:1.25rem">${title}</h1><p>${text}</p><div class="other" lang="${second}"><p><strong>${otherTitle}</strong><br>${otherText}</p></div></main></body></html>`)
}

const server = createServer((request, response) => {
  const found = target(request.headers.host)
  // The builder asks for its own language with ?lang=id or ?lang=en; others get
  // their browser's.
  const asked = /[?&]lang=(id|en)(?:&|$)/.exec(request.url || '')?.[1]
  const language = asked ?? request.headers['accept-language']
  if (!found) return page(response, 404, 'missing', false, language)
  if (found.expired) return page(response, 410, 'expired', false, language)
  if (found.sample) return sample(found.sample, request.url, response)
  if (!found.container) return page(response, 404, 'missing', false, language)

  const headers = { ...request.headers }
  for (const name of Object.keys(headers)) if (name.startsWith('x-forwarded-')) delete headers[name]
  headers['x-forwarded-for'] = request.socket.remoteAddress || ''
  headers['x-forwarded-host'] = String(request.headers.host || '')
  const upstream = forward({ host: found.container, port: 3000, method: request.method, path: request.url, headers, timeout: 30000 }, (reply) => {
    const out = { ...reply.headers }
    if (found.preview) out['cache-control'] = 'no-store'
    response.writeHead(reply.statusCode || 502, out)
    reply.pipe(response)
  })
  upstream.on('timeout', () => upstream.destroy(new Error('timeout')))
  upstream.on('error', () => {
    if (response.headersSent) return response.destroy()
    if (found.preview) page(response, 503, 'starting', true, language)
    else page(response, 503, 'unavailable', true, language)
  })
  request.pipe(upstream)
})
server.listen(PORT, () => console.log(`gateway on :${PORT}, previews *.${PREVIEW_DOMAIN}, sites *.${SITES_DOMAIN}`))
