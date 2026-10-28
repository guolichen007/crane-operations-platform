#!/bin/sh
set -eu
umask 077
app_password=$(cat /run/crane-init/app_db_password)
keycloak_password=$(cat /run/crane-init/keycloak_db_password)
for value in "$app_password" "$keycloak_password"; do
  case "$value" in *[!0-9a-f]*|'') echo 'Crane DB secret format rejected' >&2; exit 1;; esac
  [ "${#value}" -eq 64 ] || { echo 'Crane DB secret length rejected' >&2; exit 1; }
done
if ! psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --set ON_ERROR_STOP=1 --quiet >/dev/null 2>/dev/null <<SQL
SET log_statement=none;
SET log_min_error_statement=panic;
SET log_error_verbosity=terse;
CREATE ROLE crane_app LOGIN PASSWORD '$app_password';
CREATE ROLE crane_keycloak LOGIN PASSWORD '$keycloak_password';
CREATE DATABASE crane_app OWNER crane_app;
CREATE DATABASE crane_keycloak OWNER crane_keycloak;
REVOKE ALL ON DATABASE crane_app FROM PUBLIC;
REVOKE ALL ON DATABASE crane_keycloak FROM PUBLIC;
GRANT CONNECT ON DATABASE crane_app TO crane_app;
GRANT CONNECT ON DATABASE crane_keycloak TO crane_keycloak;
SQL
then
  echo 'Crane isolated database initialization failed' >&2
  exit 1
fi
unset app_password keycloak_password value
rm -f /run/crane-init/app_db_password /run/crane-init/keycloak_db_password
