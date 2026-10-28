#!/bin/sh
set -eu
umask 077
# Root copies only the two init-only credentials before the official entrypoint drops UID.
# The copies never enter an image layer and are removed by the init script.
if [ ! -s "${PGDATA:-/var/lib/postgresql/data}/PG_VERSION" ]; then
  mkdir -p /run/crane-init
  chmod 700 /run/crane-init
  for name in app_db_password keycloak_db_password; do
    cp "/run/secrets/$name" "/run/crane-init/$name"
    chmod 600 "/run/crane-init/$name"
    chown postgres:postgres "/run/crane-init/$name"
  done
  chown postgres:postgres /run/crane-init
fi
exec /usr/local/bin/docker-entrypoint.sh "$@"
