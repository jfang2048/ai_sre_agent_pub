#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
if [[ -n "${SRE_TEST_POSTGRES_DSN:-}" ]]; then
  exec go -C "${ROOT_DIR}/backend" test ./internal/controller/ingest -run 'TestInbox' -count=1 -v
fi

# A private Unix socket needs neither root nor a cluster nor network exposure.
PG_BINDIR="${SRE_TEST_PG_BINDIR:-}"
if [[ -z "${PG_BINDIR}" ]] && command -v pg_config >/dev/null 2>&1; then
  PG_BINDIR="$(pg_config --bindir)"
fi
if [[ ! -x "${PG_BINDIR}/initdb" || ! -x "${PG_BINDIR}/pg_ctl" ]]; then
  echo "PostgreSQL test NOT RUN: set SRE_TEST_POSTGRES_DSN or install initdb/pg_ctl" >&2
  exit 1
fi
TEST_DIR="$(mktemp -d /tmp/sre-ingest-pg.XXXXXX)"
STARTED=0
cleanup() {
  if [[ "${STARTED}" == 1 ]]; then
    "${PG_BINDIR}/pg_ctl" -D "${TEST_DIR}/data" -m fast -w stop >/dev/null
  fi
  echo "PostgreSQL test logs: ${TEST_DIR}"
}
trap cleanup EXIT
"${PG_BINDIR}/initdb" -D "${TEST_DIR}/data" -A trust -U postgres --no-locale >"${TEST_DIR}/init.log"
"${PG_BINDIR}/pg_ctl" -D "${TEST_DIR}/data" -l "${TEST_DIR}/server.log" -o "-h '' -k '${TEST_DIR}'" -w start >/dev/null
STARTED=1
export SRE_TEST_POSTGRES_DSN="host=${TEST_DIR} user=postgres dbname=postgres sslmode=disable"
go -C "${ROOT_DIR}/backend" test ./internal/controller/ingest -run 'TestInbox' -count=1 -v
