-- G4-S5-R1-F: recoverable account operations.
--
-- An account operation is written down before Keycloak is called and moves
-- through recorded steps. A claim (lease token + expiry) says which process is
-- driving it. When a process dies the lease runs out and the recovery job (or an
-- administrator's "核对") takes the operation over, reads the real Keycloak state
-- and either completes it, compensates it, or parks it as REVIEW (待核对). Nothing
-- is replayed blindly: creations, password resets and role changes with an
-- unknown outcome are reconciled from Keycloak, never repeated.
--
-- 00006 is not modified; the constraints and the guard function it created are
-- replaced here.

ALTER TABLE account_operations
 ADD COLUMN step text NOT NULL DEFAULT 'OPENED'
  CHECK (step IN ('OPENED','KC_CREATE','BOUND','CREDENTIAL','ROLE','ENABLE','KC_DISABLE','KC_USERNAME','KC_PASSWORD','FINAL')),
 ADD COLUMN lease_token uuid,
 ADD COLUMN lease_expires_at timestamptz,
 ADD COLUMN attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
 -- Fingerprint of the request (operation, member, phone, reason): the same
 -- idempotency key with a different request is refused.
 ADD COLUMN request_hash text CHECK (request_hash IS NULL OR request_hash ~ '^[0-9a-f]{64}$'),
 -- The request's reason, so recovery can write the audit row it would have written.
 ADD COLUMN reason text NOT NULL DEFAULT '' CHECK (length(reason) <= 600),
 -- Logins (11 digits) the operation is about: the one it sets (create, change
 -- phone) and the one it replaces. They let recovery find the Keycloak user even
 -- when the process died before the user id was recorded.
 ADD COLUMN target_login text CHECK (target_login IS NULL OR target_login ~ '^1[3-9][0-9]{9}$'),
 ADD COLUMN previous_login text CHECK (previous_login IS NULL OR previous_login ~ '^1[3-9][0-9]{9}$'),
 -- The Keycloak user was created by this operation (compensation may delete it);
 -- a user that existed before is only ever disabled.
 ADD COLUMN created_user boolean NOT NULL DEFAULT false,
 ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

-- Rows still STARTED from 00006 have no step history: recovery takes them at once.
UPDATE account_operations SET lease_expires_at = now() WHERE state = 'STARTED';

ALTER TABLE account_operations DROP CONSTRAINT account_operations_state_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_state_check
 CHECK (state IN ('STARTED','DONE','FAILED','COMPENSATED','REVIEW'));
-- A running operation always has a claim that can expire.
ALTER TABLE account_operations ADD CONSTRAINT account_operations_claim_check
 CHECK (state <> 'STARTED' OR lease_expires_at IS NOT NULL);

-- One unresolved operation per member (running, or waiting for review), and one
-- per login: two requests can never race for the same phone.
DROP INDEX account_operations_one_open;
CREATE UNIQUE INDEX account_operations_one_unresolved ON account_operations(staff_id) WHERE state IN ('STARTED','REVIEW');
CREATE UNIQUE INDEX account_operations_one_login ON account_operations(target_login)
 WHERE state IN ('STARTED','REVIEW') AND target_login IS NOT NULL;
CREATE INDEX account_operations_recovery ON account_operations(lease_expires_at) WHERE state = 'STARTED';

-- REVIEW (待核对): Keycloak and Baobei could not be proven consistent. The login
-- gate refuses CREATING and REVIEW, so the member cannot sign in until an
-- administrator's reconcile resolves it.
ALTER TABLE app_users DROP CONSTRAINT app_users_account_state_check;
ALTER TABLE app_users ADD CONSTRAINT app_users_account_state_check
 CHECK (account_state IN ('NONE','CREATING','READY','DISABLED','FAILED','REVIEW'));

-- Business subjects stay permanent once bound. The account service may bind a
-- placeholder to a Keycloak UUID, or undo its own binding of a member who has
-- never signed in (so no project, approval or audit row can name the UUID) while
-- the account is CREATING or REVIEW.
CREATE OR REPLACE FUNCTION staff_subject_guard() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF NEW.subject IS DISTINCT FROM OLD.subject THEN
  IF OLD.subject = 'pending:' || OLD.staff_id::text
     AND NEW.subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
   RETURN NEW;
  END IF;
  IF NEW.subject = 'pending:' || OLD.staff_id::text AND OLD.account_state IN ('CREATING','REVIEW')
     AND OLD.last_login_at IS NULL THEN
   RETURN NEW;
  END IF;
  RAISE EXCEPTION 'bound business subject is immutable' USING ERRCODE = 'BB002';
 END IF;
 RETURN NEW;
END $$;
REVOKE EXECUTE ON FUNCTION staff_subject_guard() FROM PUBLIC;
