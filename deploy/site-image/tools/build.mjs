// Builds a Website Builder site and proves it works, inside the sandbox:
//
//   node /opt/site/tools/build.mjs        (working directory: the site, at /workspace)
//
// Steps: type check, build the pages, render every page once, bundle the server,
// start it and call it. The first step that fails prints its problems to stderr under
// a "### <STEP>" heading and the script exits with 1. On success the finished site is
// in /workspace/.output (server.mjs, public/) and a JSON summary goes to stdout.
//
// The container has no network. Everything used here comes from the image.
import { cp, mkdir, rm, symlink, readdir } from 'node:fs/promises'
import { readFileSync } from 'node:fs'
import { existsSync } from 'node:fs'
import { join, relative } from 'node:path'
import { spawn } from 'node:child_process'

const SOURCE = process.cwd()
const WORK = '/tmp/site-build'
const OUT = join(WORK, '.output')
const FINAL = join(SOURCE, '.output')
const TOOLCHAIN = '/opt/site/node_modules'
const SMOKE_PORT = 4310

const warnings = []

function report(step, body) {
  process.stderr.write(`\n### ${step}\n${String(body).trim()}\n`)
  process.exit(1)
}

// Paths in messages are made relative to the site, so the agent sees web/src/pages/Home.tsx.
function local(text) {
  return String(text ?? '').split(WORK + '/').join('').split(SOURCE + '/').join('')
}

async function prepare() {
  await rm(WORK, { recursive: true, force: true })
  await cp(SOURCE, WORK, {
    recursive: true,
    filter: (path) => {
      const rel = relative(SOURCE, path).replaceAll('\\', '/')
      return !(rel === '.output' || rel.startsWith('.output/') || rel.includes('node_modules') || rel.startsWith('.git'))
    },
  })
  await symlink(TOOLCHAIN, join(WORK, 'node_modules'), 'dir')
  for (const required of ['web/index.html', 'web/src/main.tsx', 'web/src/routes.tsx', 'server/index.js']) {
    if (!existsSync(join(WORK, required))) report('MISSING FILE', `${required} is missing.`)
  }
}

async function sourceFiles(dir) {
  const found = []
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) found.push(...(await sourceFiles(path)))
    else if (/\.(tsx?|d\.ts)$/.test(entry.name)) found.push(path)
  }
  return found
}

async function typeCheck() {
  const ts = (await import('typescript')).default
  const files = await sourceFiles(join(WORK, 'web/src'))
  const program = ts.createProgram(files, {
    jsx: ts.JsxEmit.ReactJSX,
    target: ts.ScriptTarget.ES2022,
    module: ts.ModuleKind.ESNext,
    moduleResolution: ts.ModuleResolutionKind.Bundler,
    lib: ['lib.es2023.d.ts', 'lib.dom.d.ts', 'lib.dom.iterable.d.ts'],
    strict: false,
    noImplicitAny: false,
    skipLibCheck: true,
    noEmit: true,
    isolatedModules: true,
    resolveJsonModule: true,
    esModuleInterop: true,
    typeRoots: [join(WORK, 'node_modules/@types')],
    types: ['react', 'react-dom'],
  })
  const problems = ts.getPreEmitDiagnostics(program).filter((d) => d.category === ts.DiagnosticCategory.Error)
  if (problems.length === 0) return
  const lines = problems.slice(0, 30).map((d) => {
    const message = ts.flattenDiagnosticMessageText(d.messageText, '\n')
    if (!d.file) return message
    const { line, character } = d.file.getLineAndCharacterOfPosition(d.start ?? 0)
    return `${local(d.file.fileName)}:${line + 1}:${character + 1} ${message}`
  })
  if (problems.length > 30) lines.push(`... and ${problems.length - 30} more`)
  report('TYPE ERRORS', lines.join('\n'))
}

async function plugins() {
  const react = (await import('@vitejs/plugin-react')).default
  const tailwind = (await import('@tailwindcss/vite')).default
  return [react(), tailwind()]
}

async function buildPages() {
  const { build } = await import('vite')
  try {
    await build({
      configFile: false,
      root: join(WORK, 'web'),
      logLevel: 'error',
      cacheDir: '/tmp/vite-cache',
      plugins: await plugins(),
      build: { outDir: join(OUT, 'public'), emptyOutDir: true, reportCompressedSize: false },
    })
  } catch (error) {
    report('BUILD ERROR', local(error?.message || error))
  }
}

// Browser objects a page may touch while it renders. Rendering here only proves that
// the code runs; problems that come from these stand-ins are ignored.
function browserStandIns(path) {
  const noop = () => {}
  const element = () => ({ classList: { add: noop, remove: noop, toggle: noop }, style: {}, setAttribute: noop, appendChild: noop, addEventListener: noop, removeEventListener: noop })
  globalThis.window = {
    location: { pathname: path, search: '', hash: '', href: `http://site.local${path}` },
    history: { pushState: noop, replaceState: noop },
    addEventListener: noop,
    removeEventListener: noop,
    dispatchEvent: noop,
    scrollTo: noop,
    matchMedia: () => ({ matches: false, addEventListener: noop, removeEventListener: noop }),
    innerWidth: 1280,
    innerHeight: 800,
  }
  globalThis.document = { title: '', body: element(), documentElement: element(), querySelector: () => null, getElementById: () => null, addEventListener: noop, removeEventListener: noop, createElement: element }
}
const STAND_IN = /window|document|localStorage|sessionStorage|navigator|matchMedia|IntersectionObserver|ResizeObserver|requestAnimationFrame/

async function renderPages() {
  const { createServer } = await import('vite')
  const { createElement } = await import('react')
  const { renderToString } = await import('react-dom/server')
  const server = await createServer({
    configFile: false,
    root: join(WORK, 'web'),
    logLevel: 'silent',
    cacheDir: '/tmp/vite-cache-ssr',
    appType: 'custom',
    server: { middlewareMode: true, hmr: false, ws: false },
    plugins: await plugins(),
  })
  const paths = []
  try {
    let routes, Layout
    try {
      routes = (await server.ssrLoadModule('/src/routes.tsx')).routes
      Layout = (await server.ssrLoadModule('/src/components/Layout.tsx')).default
    } catch (error) {
      server.ssrFixStacktrace(error)
      report('RENDER ERROR', local(error?.stack || error))
    }
    if (!Array.isArray(routes) || routes.length === 0) report('RENDER ERROR', 'web/src/routes.tsx exports no routes.')
    for (const route of routes) {
      if (route.path !== '*') paths.push(route.path)
      browserStandIns(route.path === '*' ? '/missing-page' : route.path)
      try {
        renderToString(createElement(Layout, null, createElement(route.component)))
      } catch (error) {
        server.ssrFixStacktrace(error)
        const text = local(error?.stack || error)
        if (STAND_IN.test(String(error?.message))) {
          warnings.push(`${route.path}: ${String(error?.message).slice(0, 200)}`)
          continue
        }
        const own = text.split('\n').filter((line, index) => index === 0 || !line.includes('node_modules'))
        report(`RENDER ERROR on page ${route.path}`, own.slice(0, 12).join('\n'))
      }
    }
  } finally {
    delete globalThis.window
    delete globalThis.document
    await server.close()
  }
  return paths
}

async function bundleServer() {
  const esbuild = await import('esbuild')
  try {
    await esbuild.build({
      entryPoints: [join(WORK, 'server/index.js')],
      outfile: join(OUT, 'server.mjs'),
      bundle: true,
      platform: 'node',
      format: 'esm',
      target: 'node24',
      loader: { '.sql': 'text' },
      logLevel: 'silent',
      nodePaths: [TOOLCHAIN],
      banner: { js: "import { createRequire as __siteRequire } from 'node:module'; const require = __siteRequire(import.meta.url);" },
    })
  } catch (error) {
    const lines = (error.errors || []).map((e) => `${local(e.location?.file ?? '')}:${e.location?.line ?? ''} ${e.text}`)
    report('SERVER BUILD ERROR', lines.join('\n') || local(error.message))
  }
}

async function smokeTest(paths) {
  const output = []
  const child = spawn(process.execPath, [join(OUT, 'server.mjs')], {
    cwd: OUT,
    env: { PATH: process.env.PATH, PORT: String(SMOKE_PORT), HOST: '127.0.0.1', DATA_DIR: '/tmp/site-smoke-data', NODE_ENV: 'production' },
  })
  child.stdout.on('data', (chunk) => output.push(String(chunk)))
  child.stderr.on('data', (chunk) => output.push(String(chunk)))
  let exited = false
  child.on('exit', () => (exited = true))
  const base = `http://127.0.0.1:${SMOKE_PORT}`
  const fail = (message) => {
    child.kill('SIGKILL')
    report('SERVER ERROR', `${message}\n${local(output.join('').slice(-3000))}`)
  }
  let up = false
  for (let attempt = 0; attempt < 50 && !exited; attempt++) {
    try {
      if ((await fetch(`${base}/api/health`)).ok) {
        up = true
        break
      }
    } catch {}
    await new Promise((resolve) => setTimeout(resolve, 200))
  }
  if (!up) fail('The server did not start or /api/health did not answer.')
  for (const path of paths) {
    const response = await fetch(base + path).catch(() => null)
    const body = response ? await response.text() : ''
    if (!response || response.status !== 200 || !body.includes('id="root"')) fail(`GET ${path} did not return the page (status ${response?.status ?? 'none'}).`)
  }
  const form = await fetch(`${base}/api/forms/build-check`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ name: 'Build check' }) }).catch(() => null)
  if (!form || !form.ok) fail(`POST /api/forms/build-check failed (status ${form?.status ?? 'none'}).`)
  // A guest book form shows its name and message on the site, and nothing else.
  let guestbooks = []
  try {
    guestbooks = (JSON.parse(readFileSync(join(WORK, 'site.json'), 'utf8')).brief?.forms ?? []).filter((f) => f && f.guestbook === true).map((f) => String(f.name))
  } catch {}
  for (const name of guestbooks) {
    const sent = await fetch(`${base}/api/forms/${name}`, { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify({ name: 'Build check', message: 'Build check message', private_field: 'private-value' }) }).catch(() => null)
    if (!sent || !sent.ok) fail(`POST /api/forms/${name} failed (status ${sent?.status ?? 'none'}).`)
    const list = await fetch(`${base}/api/forms/${name}/public`).catch(() => null)
    const text = list ? await list.text() : ''
    if (!list || !list.ok || !text.includes('Build check message')) fail(`GET /api/forms/${name}/public did not list the message just sent (status ${list?.status ?? 'none'}).`)
    if (text.includes('private-value')) fail(`GET /api/forms/${name}/public shows a private field.`)
  }
  child.kill('SIGTERM')
}

async function main() {
  await prepare()
  process.chdir(join(WORK, 'web'))
  await typeCheck()
  await buildPages()
  const paths = await renderPages()
  await bundleServer()
  await smokeTest(paths)
  await rm(FINAL, { recursive: true, force: true })
  await mkdir(FINAL, { recursive: true })
  await cp(OUT, FINAL, { recursive: true })
  process.stdout.write(JSON.stringify({ ok: true, pages: paths, warnings }) + '\n')
}

main().catch((error) => report('BUILD TOOL ERROR', local(error?.stack || error)))
