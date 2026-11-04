ALTER TABLE app_users
 ADD COLUMN staff_id uuid NOT NULL DEFAULT gen_random_uuid() UNIQUE,
 ADD COLUMN work_email text,
 ADD COLUMN staff_status text NOT NULL DEFAULT 'ACTIVE',
 ADD COLUMN staff_version integer NOT NULL DEFAULT 1 CHECK(staff_version>0),
 ADD COLUMN staff_note text NOT NULL DEFAULT '',
 ADD COLUMN staff_permissions smallint NOT NULL DEFAULT 7 CHECK(staff_permissions BETWEEN 0 AND 7),
 ADD COLUMN staff_managed boolean NOT NULL DEFAULT false,
 ADD COLUMN last_login_at timestamptz;
UPDATE app_users SET staff_status='SUSPENDED' WHERE NOT active;
ALTER TABLE app_users ADD CONSTRAINT staff_state CHECK (
 staff_status IN ('PENDING_PROVISION','PENDING_ACTIVATION','ACTIVE','SUSPENDED','PROVISION_FAILED','ARCHIVED')
 AND active = (staff_status='ACTIVE')
);
ALTER TABLE app_users ADD CONSTRAINT staff_email_canonical CHECK (
 work_email IS NULL OR (work_email=lower(work_email) AND length(work_email)<=254)
);
CREATE UNIQUE INDEX staff_email_unique ON app_users(work_email) WHERE work_email IS NOT NULL;
CREATE INDEX staff_team_role ON app_users(team_id,role,staff_status);
ALTER TABLE teams ADD COLUMN staff_version integer NOT NULL DEFAULT 1;
CREATE TABLE staff_sessions (
 token_hash text PRIMARY KEY REFERENCES app_sessions(token_hash) ON DELETE CASCADE,
 subject text NOT NULL REFERENCES app_users,
 reauthenticated_at timestamptz
);

CREATE TABLE staff_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 target_id uuid NOT NULL,
 actor text NOT NULL,
 action text NOT NULL,
 reason text NOT NULL,
 version integer NOT NULL,
 at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX staff_audit_target ON staff_audit(target_id,at DESC,id DESC);
CREATE TABLE staff_outbox (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 staff_id uuid NOT NULL REFERENCES app_users(staff_id),
 staff_version integer NOT NULL,
 operation text NOT NULL CHECK(operation IN ('PROVISION','SUSPEND')),
 status text NOT NULL DEFAULT 'PENDING' CHECK(status IN ('PENDING','ACKNOWLEDGED','FAILED','SUPERSEDED')),
 created_at timestamptz NOT NULL DEFAULT now(),
 processed_at timestamptz,
 receipt_hash text,
 UNIQUE(staff_id,staff_version,operation)
);
CREATE INDEX staff_outbox_pending ON staff_outbox(staff_id,created_at) WHERE status='PENDING';
CREATE TABLE staff_handoffs (
 staff_id uuid NOT NULL REFERENCES app_users(staff_id),
 project_id uuid NOT NULL REFERENCES intake_projects,
 queued_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(staff_id,project_id)
);

CREATE FUNCTION staff_legacy_state() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF NOT NEW.staff_managed THEN
  NEW.staff_status := CASE WHEN NEW.active THEN 'ACTIVE' ELSE 'SUSPENDED' END;
  IF TG_OP='UPDATE' AND (OLD.display_name,OLD.role,OLD.team_id,OLD.active)
    IS DISTINCT FROM (NEW.display_name,NEW.role,NEW.team_id,NEW.active) THEN
   NEW.staff_version := OLD.staff_version+1;
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER staff_legacy_state BEFORE INSERT OR UPDATE OF active,role,team_id,display_name ON app_users
 FOR EACH ROW EXECUTE FUNCTION staff_legacy_state();

CREATE FUNCTION staff_keep_admin() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF OLD.role='baobei_admin' AND OLD.active AND (NOT NEW.active OR NEW.role<>'baobei_admin') THEN
  PERFORM pg_advisory_xact_lock(727105);
  IF NOT EXISTS (SELECT 1 FROM app_users WHERE role='baobei_admin' AND active AND subject<>OLD.subject) THEN
   RAISE EXCEPTION 'last active administrator' USING ERRCODE='BB001';
  END IF;
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER staff_keep_admin BEFORE UPDATE OF active,role ON app_users
 FOR EACH ROW EXECUTE FUNCTION staff_keep_admin();
REVOKE EXECUTE ON FUNCTION staff_legacy_state(),staff_keep_admin() FROM PUBLIC;
