#!/bin/sh
set -eu
if [ ! -s /var/lib/postgresql/data/PG_VERSION ]; then
  mkdir -p /run/crane-init
  cp /fixture/secrets/app_db_password /run/crane-init/app_db_password
  cp /fixture/secrets/keycloak_db_password /run/crane-init/keycloak_db_password
  chown -R postgres:postgres /run/crane-init
  chmod 0700 /run/crane-init
  chmod 0600 /run/crane-init/*
fi
exec /usr/local/bin/docker-entrypoint.sh "$@"
