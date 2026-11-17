-- V1.1 R3: first-login name confirmation.
--
-- 00001-00009 are not modified. A member confirms their own display name once
-- before using business functions; logging in, changing the password and
-- signing out never depend on it. Only the display name can change here: the
-- subject, staff id, role and team are never touched. Every confirmation or
-- change is an immutable profile_audit row written by the same definer
-- function that updates the name, so a name never changes without its record.
ALTER TABLE app_users
 ADD COLUMN profile_completed_at timestamptz,
 ADD COLUMN profile_name_source text NOT NULL DEFAULT 'UNCONFIRMED'
  CHECK (profile_name_source IN ('UNCONFIRMED','SELF_CONFIRMED','BACKFILL_VERIFIED','DEV_FIXTURE')),
 ADD COLUMN profile_version integer NOT NULL DEFAULT 1 CHECK (profile_version > 0),
 ADD CONSTRAINT profile_completion CHECK ((profile_completed_at IS NULL) = (profile_name_source = 'UNCONFIRMED'));

CREATE TABLE profile_audit (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 staff_id uuid NOT NULL REFERENCES app_users(staff_id),
 actor_subject text NOT NULL,
 action text NOT NULL CHECK (action IN ('profile.confirm','profile.rename','profile.backfill')),
 old_name text NOT NULL,
 new_name text NOT NULL,
 reason text NOT NULL CHECK (length(reason) <= 600),
 profile_version integer NOT NULL CHECK (profile_version > 0),
 at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_audit_staff ON profile_audit(staff_id, at DESC);
CREATE TRIGGER profile_audit_immutable BEFORE UPDATE OR DELETE ON profile_audit
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();

-- Trusted names (explicit rule, no name pattern matching): active members who
-- already signed in and whose record came from a controlled administrator
-- path — staff management with a verified activation, or the root-only
-- identity manifest (not staff managed). Never-activated invitations and
-- members who never signed in confirm on their next login. Names are never
-- rewritten by the migration; the list is reviewable in profile_audit.
INSERT INTO profile_audit(staff_id, actor_subject, action, old_name, new_name, reason, profile_version)
 SELECT u.staff_id, 'migration:00010', 'profile.backfill', u.display_name, u.display_name,
  CASE WHEN u.staff_managed THEN '管理员开户并经企业身份激活的成员，姓名视为已核实' ELSE '受控身份清单导入并已登录的成员，姓名视为已核实' END, 1
 FROM app_users u
 WHERE u.staff_status = 'ACTIVE' AND u.last_login_at IS NOT NULL
  AND (NOT u.staff_managed OR EXISTS (SELECT 1 FROM staff_audit a WHERE a.target_id = u.staff_id AND a.action = 'staff.activate'));
UPDATE app_users u SET profile_completed_at = now(), profile_name_source = 'BACKFILL_VERIFIED'
 WHERE EXISTS (SELECT 1 FROM profile_audit p WHERE p.staff_id = u.staff_id AND p.action = 'profile.backfill');

-- The only way the business API changes a name: the caller's own row, a valid
-- name, the expected profile version, one audit row. Role, team, subject and
-- staff id are not parameters.
CREATE FUNCTION confirm_own_profile(p_subject text, p_name text, p_reason text, p_version integer)
 RETURNS integer LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
DECLARE
 u app_users%ROWTYPE;
BEGIN
 IF p_name IS NULL OR p_name <> btrim(p_name) OR length(p_name) NOT BETWEEN 1 AND 64
    OR p_name ~ '^[0-9[:space:]]+$' OR p_name ~ '[[:cntrl:]]' THEN
  RAISE EXCEPTION 'invalid display name' USING ERRCODE = 'BB010';
 END IF;
 IF p_reason IS NULL OR length(p_reason) > 600 THEN
  RAISE EXCEPTION 'invalid reason' USING ERRCODE = 'BB010';
 END IF;
 SELECT * INTO u FROM app_users WHERE subject = p_subject AND active FOR UPDATE;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'inactive member' USING ERRCODE = 'BB011';
 END IF;
 IF u.profile_version <> p_version THEN
  RAISE EXCEPTION 'profile version conflict' USING ERRCODE = 'BB012';
 END IF;
 UPDATE app_users SET display_name = p_name, profile_completed_at = coalesce(profile_completed_at, now()),
  profile_name_source = CASE WHEN profile_name_source = 'UNCONFIRMED' THEN 'SELF_CONFIRMED' ELSE profile_name_source END,
  profile_version = profile_version + 1
  WHERE staff_id = u.staff_id;
 INSERT INTO profile_audit(staff_id, actor_subject, action, old_name, new_name, reason, profile_version)
  VALUES (u.staff_id, p_subject, CASE WHEN u.profile_completed_at IS NULL THEN 'profile.confirm' ELSE 'profile.rename' END,
   u.display_name, p_name, coalesce(p_reason, ''), u.profile_version + 1);
 RETURN u.profile_version + 1;
END $$;
REVOKE EXECUTE ON FUNCTION confirm_own_profile(text, text, text, integer) FROM PUBLIC;
