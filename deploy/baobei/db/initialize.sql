-- PostgreSQL entrypoint runs this once on an empty dedicated Baobei volume.
-- Logins stay passwordless until the operator provisions separate SCRAM passwords.
BEGIN;
CREATE ROLE baobei_migrator LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE baobei_runtime LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
CREATE ROLE baobei_maintenance LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;

REVOKE ALL ON DATABASE baobei FROM PUBLIC;
REVOKE CONNECT, TEMPORARY ON DATABASE postgres, template1 FROM PUBLIC;
GRANT CONNECT ON DATABASE baobei TO baobei_migrator, baobei_runtime, baobei_maintenance;
REVOKE ALL ON SCHEMA public FROM PUBLIC;
GRANT USAGE, CREATE ON SCHEMA public TO baobei_migrator;
GRANT USAGE ON SCHEMA public TO baobei_runtime, baobei_maintenance;
CREATE EXTENSION pg_trgm WITH SCHEMA public;
COMMIT;
