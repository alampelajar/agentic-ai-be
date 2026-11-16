// The site's database: one SQLite file in DATA_DIR. Fixed by the template.
import { DatabaseSync } from 'node:sqlite'
import { mkdirSync } from 'node:fs'
import { join } from 'node:path'
import schema from './schema.sql'

export function openDatabase() {
  const dir = process.env.DATA_DIR || './data'
  mkdirSync(dir, { recursive: true })
  const db = new DatabaseSync(join(dir, 'site.db'))
  db.exec('PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;')
  db.exec(schema)
  return db
}
