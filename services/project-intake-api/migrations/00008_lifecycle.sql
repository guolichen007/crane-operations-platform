-- G4-S5-R1-F2 + R2-P1: identity history guard and three business lifecycles.
--
-- 00001-00007 are not modified; constraints and functions they created are
-- replaced here.

-- 1. A business subject is "referenced" once any project, review, follow-up,
--    participation, immutable evidence or audit row names it. Such a subject is
--    permanent: no compensation, revocation or tool may move it back to a
--    placeholder (BB002). last_login_at alone is not enough evidence.
CREATE FUNCTION staff_subject_referenced(s text) RETURNS boolean LANGUAGE sql STABLE
 SECURITY DEFINER SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM intake_projects WHERE owner_subject=s OR created_by_subject=s OR draft_custodian_subject=s OR submitted_by_subject=s)
  OR EXISTS(SELECT 1 FROM project_activities WHERE subject=s)
  OR EXISTS(SELECT 1 FROM approvals WHERE subject=s)
  OR EXISTS(SELECT 1 FROM ownership_history WHERE from_subject=s OR to_subject=s OR actor_subject=s)
  OR EXISTS(SELECT 1 FROM duplicate_cases WHERE decided_by_subject=s)
  OR EXISTS(SELECT 1 FROM project_submissions WHERE actor_subject=s OR creator_subject=s OR owner_subject=s OR participants ? s)
  OR EXISTS(SELECT 1 FROM project_participations WHERE actor_subject=s OR owner_subject=s)
  OR EXISTS(SELECT 1 FROM project_create_requests WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM audit_events WHERE subject=s OR position('"'||s||'"' in details::text)>0)
  OR EXISTS(SELECT 1 FROM staff_audit WHERE actor=s)
  OR EXISTS(SELECT 1 FROM account_operations WHERE actor_subject=s)
$$;
REVOKE EXECUTE ON FUNCTION staff_subject_referenced(text) FROM PUBLIC;

-- 2. Staff lifecycle: REVOKED (已撤销) ends an invitation that never completed.
--    The row, its staff id and its audit history stay; the phone and e-mail are
--    released so the person can be invited again.
ALTER TABLE app_users DROP CONSTRAINT staff_state;
ALTER TABLE app_users ADD CONSTRAINT staff_state CHECK (
 staff_status IN ('PENDING_PROVISION','PENDING_ACTIVATION','ACTIVE','SUSPENDED','PROVISION_FAILED','ARCHIVED','REVOKED')
 AND active = (staff_status='ACTIVE')
);

-- The account service may bind a placeholder to a Keycloak UUID, or move a bound
-- subject back to the member's placeholder only while the binding is unfinished
-- (CREATING/REVIEW), the member never signed in, and nothing in the business
-- history names the subject. A revoked invitation keeps its subject.
CREATE OR REPLACE FUNCTION staff_subject_guard() RETURNS trigger LANGUAGE plpgsql
 SECURITY DEFINER SET search_path FROM CURRENT AS $$
BEGIN
 IF NEW.subject IS DISTINCT FROM OLD.subject THEN
  IF OLD.subject = 'pending:' || OLD.staff_id::text
     AND NEW.subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
   RETURN NEW;
  END IF;
  IF NEW.subject = 'pending:' || OLD.staff_id::text
     AND OLD.account_state IN ('CREATING','REVIEW')
     AND OLD.last_login_at IS NULL
     AND NOT staff_subject_referenced(OLD.subject) THEN
   RETURN NEW;
  END IF;
  RAISE EXCEPTION 'bound business subject is immutable' USING ERRCODE = 'BB002';
 END IF;
 RETURN NEW;
END $$;
REVOKE EXECUTE ON FUNCTION staff_subject_guard() FROM PUBLIC;

-- An identity link is never rewritten in place: a different mapping is a new row
-- (refused by the primary key while the old one exists).
CREATE FUNCTION identity_link_immutable() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 RAISE EXCEPTION 'identity link is immutable' USING ERRCODE = 'BB003';
END $$;
CREATE TRIGGER identity_link_immutable BEFORE UPDATE ON identity_links
 FOR EACH ROW EXECUTE FUNCTION identity_link_immutable();
REVOKE EXECUTE ON FUNCTION identity_link_immutable() FROM PUBLIC;

-- Revocation is an account operation when a login account has to be removed.
ALTER TABLE account_operations DROP CONSTRAINT account_operations_operation_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_operation_check
 CHECK (operation IN ('CREATE','RESET_PASSWORD','CHANGE_PHONE','DISABLE','ENABLE','SET_ROLE','REVOKE'));
ALTER TABLE account_operations DROP CONSTRAINT account_operations_step_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_step_check
 CHECK (step IN ('OPENED','KC_CREATE','BOUND','CREDENTIAL','ROLE','ENABLE','KC_DISABLE','KC_USERNAME','KC_PASSWORD','KC_DELETE','FINAL'));

-- 3. Teams: archive and restore. An archived team keeps its name (still unique),
--    its history and every project and approval that names it; it cannot be
--    chosen for new members.
ALTER TABLE teams
 ADD COLUMN status text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE','ARCHIVED')),
 ADD COLUMN archived_at timestamptz,
 ADD CONSTRAINT team_archive_state CHECK ((status='ARCHIVED') = (archived_at IS NOT NULL));

-- 4. Projects: a draft that was never submitted can be discarded into the
--    recycle bin (CANCELLED + discarded_at) and restored to DRAFT unchanged.
ALTER TABLE intake_projects
 ADD COLUMN discarded_at timestamptz,
 ADD COLUMN discarded_by_subject text REFERENCES app_users ON UPDATE CASCADE,
 ADD CONSTRAINT project_discard_state CHECK (
  (discarded_at IS NULL AND discarded_by_subject IS NULL)
  OR (discarded_at IS NOT NULL AND discarded_by_subject IS NOT NULL AND intake_status='CANCELLED' AND submission_revision=0)
 );
ALTER TABLE intake_projects DROP CONSTRAINT project_unassigned_draft;
ALTER TABLE intake_projects ADD CONSTRAINT project_unassigned_draft CHECK (
 owner_subject IS NOT NULL OR ((intake_status='DRAFT' OR intake_status='CANCELLED' AND discarded_at IS NOT NULL)
  AND team_id IS NULL AND protection_status='NOT_STARTED' AND protected_until IS NULL AND next_review_at IS NULL AND protection_started_at IS NULL)
);
CREATE INDEX project_discarded_idx ON intake_projects(discarded_at DESC) WHERE discarded_at IS NOT NULL;
