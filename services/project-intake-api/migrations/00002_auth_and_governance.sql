CREATE TABLE oidc_pending_logins (
 state_hash text PRIMARY KEY, nonce text NOT NULL, pkce_verifier text NOT NULL,
 binding_hash text NOT NULL, expires_at timestamptz NOT NULL, created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oidc_pending_expiry_idx ON oidc_pending_logins(expires_at);
CREATE INDEX app_sessions_expiry_idx ON app_sessions(expires_at);
ALTER TABLE intake_projects ADD COLUMN protected_until timestamptz, ADD COLUMN next_review_at timestamptz;
ALTER TABLE ownership_history ADD COLUMN actor_subject text;
ALTER TABLE audit_events ADD COLUMN details jsonb NOT NULL DEFAULT '{}';
ALTER TABLE duplicate_cases ADD COLUMN decision text NOT NULL DEFAULT 'PENDING'
 CHECK(decision IN ('PENDING','CONFIRMED_DUPLICATE','DISTINCT_APPROVED')),
 ADD COLUMN reason text, ADD COLUMN decided_by_subject text,
 ADD COLUMN decided_at timestamptz, ADD COLUMN matched_signals jsonb NOT NULL DEFAULT '[]';
CREATE INDEX duplicate_project_idx ON duplicate_cases(project_id,decision);
INSERT INTO reference_values(key,value) VALUES('protection_policy','{"warningDays":30,"expiringDays":60,"expiredDays":90}');
