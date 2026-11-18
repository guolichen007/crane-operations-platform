-- +goose Up
-- P1 personnel / account / permission management (2026-11-18). Additive over 00001..00012.
--
--   * departments: lightweight organisation units (no HR, attendance or payroll);
--   * users: personnel profile columns (unique login username, department, job title, optional
--     contact, last login, optimistic version, identity-provider sync state). users.enabled stays
--     the single login gate checked by the BFF on every request;
--   * system_memberships: an INDEPENDENT system permission domain (system_admin, account_manager).
--     It is never a project or portfolio role and grants no business data access; platform_admin
--     keeps meaning "project administrator" only;
--   * account_provisions: the durable state of the non-atomic Keycloak + database account
--     opening (idempotency key, resumable steps, no secret is ever stored);
--   * audit_logs: target user, scope and a structured change summary (never credentials).
CREATE TABLE departments (
  id text PRIMARY KEY CHECK (id ~ '^[A-Za-z0-9_-]{1,80}$'),
  name text NOT NULL CHECK (length(btrim(name)) BETWEEN 1 AND 80),
  status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','DISABLED')),
  version bigint NOT NULL DEFAULT 1,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX departments_name_unique ON departments (lower(btrim(name)));

ALTER TABLE users
  ADD COLUMN username text CHECK (username ~ '^[a-z0-9][a-z0-9_-]{1,62}$'),
  ADD COLUMN department_id text REFERENCES departments(id),
  ADD COLUMN job_title text NOT NULL DEFAULT '' CHECK (length(job_title) <= 80),
  ADD COLUMN phone text CHECK (phone IS NULL OR phone ~ '^[0-9+() -]{3,32}$'),
  ADD COLUMN email text CHECK (email IS NULL OR (length(email) <= 254 AND email ~ '^[^@\s]+@[^@\s]+$')),
  ADD COLUMN last_login_at timestamptz,
  ADD COLUMN version bigint NOT NULL DEFAULT 1,
  ADD COLUMN idp_state text NOT NULL DEFAULT 'SYNCED' CHECK (idp_state IN ('SYNCED','PENDING')),
  ADD COLUMN created_by text NOT NULL DEFAULT '';
CREATE UNIQUE INDEX users_username_unique ON users (username) WHERE username IS NOT NULL;

CREATE TABLE system_memberships (
  user_id text NOT NULL REFERENCES users(id),
  role text NOT NULL CHECK (role IN ('system_admin','account_manager')),
  granted_by text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (user_id, role)
);

CREATE TABLE account_provisions (
  id text PRIMARY KEY,
  actor text NOT NULL,
  idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 8 AND 128),
  payload_sha256 text NOT NULL,
  username text NOT NULL,
  request jsonb NOT NULL,
  state text NOT NULL CHECK (state IN ('STARTED','IDP_CREATED','BOUND','COMPLETED','FAILED','CANCELLED')),
  idp_subject text,
  failure text NOT NULL DEFAULT '',
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (actor, idempotency_key)
);
-- One in-flight or completed provision per username (failed/cancelled attempts release the name).
CREATE UNIQUE INDEX account_provisions_username_live ON account_provisions (username) WHERE state NOT IN ('FAILED','CANCELLED');

ALTER TABLE audit_logs
  ADD COLUMN target_user_id text,
  ADD COLUMN scope text,
  ADD COLUMN change jsonb;
CREATE INDEX audit_target_user_time ON audit_logs (target_user_id, observed_at) WHERE target_user_id IS NOT NULL;

-- +goose Down
-- Forward-only: dropping these tables/columns would destroy accounts, permission history and the
-- security audit trail. A genuine rollback must use a verified backup restore.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'personnel accounts migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
