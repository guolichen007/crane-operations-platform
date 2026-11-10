-- G4-S1A Preview only. Runs once, after initialize.sql and staff-role.sql, on
-- the empty dedicated Preview volume (docker-entrypoint-initdb.d/30-…).
-- Marks this database as the Preview database. The API in APP_ENV=preview
-- refuses to start (and never migrates) unless exactly this marker exists;
-- the API in APP_ENV=production refuses a database carrying it.
-- Never run this file against the production database.
BEGIN;
CREATE TABLE public.baobei_environment(
  singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
  name text NOT NULL CHECK (name = 'preview'),
  created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO public.baobei_environment(name) VALUES ('preview');
REVOKE ALL ON public.baobei_environment FROM PUBLIC;
GRANT SELECT ON public.baobei_environment TO baobei_migrator, baobei_runtime, baobei_maintenance, baobei_staff_api;
COMMIT;
