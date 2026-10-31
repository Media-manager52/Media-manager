#!/usr/bin/env bash
# Measures PostgreSQL round-trip, commit and connect latency over one transport.
# usage: latency-probe.sh <label> <host-or-socket-dir> <port> <user> <password>
set -euo pipefail
label="$1" host="$2" port="$3" user="$4" password="$5"
export PGOPTIONS="-c client_min_messages=warning"
export PGHOST="$host" PGPORT="$port" PGUSER="$user" PGPASSWORD="$password" PGCONNECT_TIMEOUT=10
dir="$(mktemp -d)"
now_ns() { date +%s%N; }
psql -X -q -d postgres -c 'DROP TABLE IF EXISTS probe_commit' -c 'CREATE TABLE probe_commit(i int)' >/dev/null
for _ in $(seq 3000); do echo 'SELECT 1;'; done >"$dir/select.sql"
for _ in $(seq 500); do echo 'INSERT INTO probe_commit VALUES (1);'; done >"$dir/insert.sql"
s=$(now_ns); psql -X -q -t -A -d postgres -f "$dir/select.sql" >/dev/null; e=$(now_ns)
select_us=$(( (e - s) / 3000 / 1000 ))
s=$(now_ns); psql -X -q -t -A -d postgres -f "$dir/insert.sql" >/dev/null; e=$(now_ns)
commit_us=$(( (e - s) / 500 / 1000 ))
s=$(now_ns); for _ in $(seq 60); do psql -X -q -t -A -d postgres -c 'SELECT 1' >/dev/null; done; e=$(now_ns)
connect_ms=$(( (e - s) / 60 / 1000000 ))
echo "PROBE ${label}: select_roundtrip=${select_us}us autocommit_insert=${commit_us}us psql_connect_and_query=${connect_ms}ms"
psql -X -q -d postgres -c 'DROP TABLE probe_commit' >/dev/null
rm -rf "$dir"
