#!/usr/bin/env bash
# Back up the database of the staging stack (compose.staging.yml).
#
#   deploy/backup.sh                 # writes to deploy/backups/
#   deploy/backup.sh /srv/backups    # or to another folder
#
# Run it from cron, for example every night at 02:00:
#   0 2 * * * /opt/agentic-ai-be/deploy/backup.sh /srv/backups >> /var/log/agentic-ai-backup.log 2>&1
#
# Each run writes one compressed dump (pg_dump custom format) and its SHA-256, checks
# that PostgreSQL can read the dump back, and deletes dumps older than KEEP_DAYS (14).
# A backup on the same server is lost with that server: copy the folder somewhere else
# too (another machine or object storage). See docs/backup-restore.md.
set -euo pipefail

here="$(cd "$(dirname "$0")" && pwd)"
dir="$(mkdir -p "${1:-$here/backups}" && cd "${1:-$here/backups}" && pwd)"
keep_days="${KEEP_DAYS:-14}"
compose=(docker compose -f "$here/compose.staging.yml" --env-file "$here/${ENV_FILE:-.env.staging}")

chmod 700 "$dir"
umask 077
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
file="$dir/agentic-ai-$stamp.dump"

# The database container already holds the user and database names; nothing secret is
# passed on the command line.
"${compose[@]}" exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --format=custom --no-owner' > "$file.partial"
mv "$file.partial" "$file"

# A dump PostgreSQL cannot list is not a backup.
"${compose[@]}" exec -T db pg_restore --list < "$file" > /dev/null
(cd "$dir" && sha256sum "$(basename "$file")" > "$(basename "$file").sha256")

find "$dir" -name 'agentic-ai-*.dump*' -type f -mtime +"$keep_days" -delete
echo "$(date -u +%FT%TZ) backup written: $file ($(du -h "$file" | cut -f1))"
