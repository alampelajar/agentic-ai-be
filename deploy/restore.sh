#!/usr/bin/env bash
# Restore a backup made by deploy/backup.sh into the staging stack's database.
#
#   deploy/restore.sh /srv/backups/agentic-ai-20261113T020000Z.dump --yes
#
# This REPLACES every table in the database with the backup's contents. The API is
# stopped while it runs, so nobody writes in the middle, and started again afterwards.
# The restore runs in one transaction: if anything fails, the database is left as it was.
set -euo pipefail

if [ "$#" -lt 1 ]; then
	echo "usage: $0 <backup.dump> --yes" >&2
	exit 2
fi
file="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
if [ "${2:-}" != "--yes" ]; then
	echo "This replaces ALL data in the database with $file." >&2
	echo "Run again with --yes to go ahead." >&2
	exit 1
fi

here="$(cd "$(dirname "$0")" && pwd)"
compose=(docker compose -f "$here/compose.staging.yml" --env-file "$here/${ENV_FILE:-.env.staging}")

if [ -f "$file.sha256" ]; then
	(cd "$(dirname "$file")" && sha256sum --check --quiet "$(basename "$file").sha256")
else
	echo "warning: no $file.sha256 to check the backup against" >&2
fi

"${compose[@]}" stop api
trap '"${compose[@]}" start api' EXIT
"${compose[@]}" exec -T db sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner --single-transaction' < "$file"
echo "$(date -u +%FT%TZ) restored $file"
