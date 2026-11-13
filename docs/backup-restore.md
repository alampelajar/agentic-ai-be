# Backup and restore

All user data (accounts, chats, workspaces' records, skills, memories, models, audit
log) is in PostgreSQL. `deploy/backup.sh` and `deploy/restore.sh` back it up and bring
it back for the staging stack in `deploy/compose.staging.yml`.

## Targets

| Measure | Target | Why |
|---|---|---|
| How much data may be lost (RPO) | 24 hours | One backup a night |
| How long a restore may take (RTO) | 1 hour | Restore itself takes seconds at today's size; the rest is finding the right dump and checking the app |
| How long backups are kept | 14 days on the server, longer off-site | `KEEP_DAYS` in `backup.sh` |

## Making backups

```bash
deploy/backup.sh /srv/backups
```

Schedule it with cron on the server (once a night):

```
0 2 * * * /opt/agentic-ai-be/deploy/backup.sh /srv/backups >> /var/log/agentic-ai-backup.log 2>&1
```

Each run writes `agentic-ai-<UTC time>.dump` and its `.sha256`, checks that PostgreSQL
can read the dump, and deletes dumps older than 14 days. The folder is readable by its
owner only: a dump holds every account's data, including password hashes and
encrypted API keys.

**Copy the folder off the server** (another machine or object storage), for example with
`rclone` or `rsync` after the backup. A backup kept only on the server is lost with it.
`AI_ENCRYPTION_KEY` must be kept as safely as the dumps, but **apart from them**: without
it the stored API keys and GitHub tokens cannot be read, and with it anyone holding a dump
can read them.

## Restoring

```bash
deploy/restore.sh /srv/backups/agentic-ai-20261113T020000Z.dump --yes
```

The script checks the SHA-256, stops the API, replaces every table in one transaction
(a failure leaves the database as it was), and starts the API again. Afterwards: sign
in, open a chat and the admin console, and check the newest audit log entries to see
which point in time the data is from.

## Restore tests

A backup that has never been restored is not known to work. Restore the newest dump
into a separate database once a month and compare row counts with the live one.

| Date | Data | Result |
|---|---|---|
| 2026-11-13 | Local development database, 32 tables, 184 KB dump | Backup 1 s, restore 1 s into a new database; row counts of all 32 tables identical. Tested with PostgreSQL 18 tools and the same `pg_dump`/`pg_restore` options as the scripts; the Docker wrapper itself is not yet run on a server |

## Not covered yet

- Uploaded files (feedback screenshots, task attachments, cloud workspaces) are files,
  not database rows. In the staging stack the API container is read-only and has no
  volume for them; decide where they live before going public, then back that place up
  too.
- Point-in-time recovery (restoring to any minute) needs WAL archiving or a managed
  database; nightly dumps restore to the night before.
