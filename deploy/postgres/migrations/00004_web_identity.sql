-- +goose Up
-- Existing development identities remain valid only in the development adapter.
ALTER TABLE users DROP CONSTRAINT users_subject_key;
ALTER TABLE users ADD COLUMN issuer text,
 ADD COLUMN enabled boolean NOT NULL DEFAULT true,
 ADD COLUMN display_name text NOT NULL DEFAULT '',
 ADD COLUMN created_at timestamptz NOT NULL DEFAULT now(),
 ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();
CREATE UNIQUE INDEX users_oidc_identity ON users(issuer,subject) WHERE issuer IS NOT NULL;
CREATE TABLE project_memberships (
 user_id text NOT NULL REFERENCES users(id),
 project_id text NOT NULL REFERENCES projects(id),
 role text NOT NULL REFERENCES roles(name) CHECK(role IN ('viewer','operator','maintainer','platform_admin')),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(user_id,project_id)
);
CREATE INDEX project_memberships_scope ON project_memberships(project_id,user_id);
CREATE TABLE web_sessions (
 session_hash bytea PRIMARY KEY CHECK(octet_length(session_hash)=32),
 user_id text NOT NULL REFERENCES users(id),
 csrf_hash bytea NOT NULL CHECK(octet_length(csrf_hash)=32),
 created_at timestamptz NOT NULL,
 last_seen_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL,
 idle_expires_at timestamptz NOT NULL,
 CHECK(expires_at>created_at), CHECK(idle_expires_at<=expires_at)
);
CREATE INDEX web_sessions_expiry ON web_sessions(expires_at,idle_expires_at);
CREATE TABLE oidc_login_transactions (
 state_hash bytea PRIMARY KEY CHECK(octet_length(state_hash)=32),
 binding_hash bytea NOT NULL CHECK(octet_length(binding_hash)=32),
 nonce_hash bytea NOT NULL CHECK(octet_length(nonce_hash)=32),
 pkce_cipher bytea NOT NULL,
 redirect_uri text NOT NULL,
 created_at timestamptz NOT NULL,
 expires_at timestamptz NOT NULL CHECK(expires_at>created_at)
);
CREATE INDEX oidc_login_expiry ON oidc_login_transactions(expires_at);
ALTER TABLE audit_logs ADD COLUMN project_id text,
 ADD COLUMN source_ip inet,
 ADD COLUMN request_id text NOT NULL DEFAULT '';
CREATE INDEX audit_project_time ON audit_logs(project_id,observed_at);
-- No memberships are inferred from user_roles, email, administrator role or inventory.
-- +goose Down
-- +goose StatementBegin
DO $$ BEGIN
 RAISE EXCEPTION 'web identity migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
