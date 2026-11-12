-- G4-S5-R1-E: dedicated Baobei realm, session issuer isolation and the account service.
--
-- 1. Identity epoch. Exactly one row names the issuer this database currently trusts
--    and an epoch number. Every session records the issuer and epoch it was created
--    under; a session is valid only while both still match. Switching realms (or
--    switching back) changes the row inside the same transaction that deletes every
--    Baobei session, so no session can survive a switch even if a delete were missed.
CREATE TABLE identity_epoch (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 issuer text NOT NULL CHECK (issuer ~ '^https?://[^ ]+$'),
 epoch integer NOT NULL CHECK (epoch > 0),
 changed_at timestamptz NOT NULL DEFAULT now(),
 changed_by text NOT NULL,
 reason text NOT NULL CHECK (length(reason) BETWEEN 1 AND 600)
);
-- Existing sessions were issued by the shared realm; mark them with epoch 1 and an
-- unknown issuer: the first start of this version under any issuer invalidates them.
ALTER TABLE app_sessions
 ADD COLUMN issuer text NOT NULL DEFAULT 'legacy:unknown',
 ADD COLUMN identity_epoch integer NOT NULL DEFAULT 0;
ALTER TABLE app_sessions ALTER COLUMN issuer DROP DEFAULT, ALTER COLUMN identity_epoch DROP DEFAULT;

-- 2. Issuer-scoped identity links. Keycloak user ids are unique per server, so a
--    member cannot keep the same UUID in a second realm on the same Keycloak while
--    the old account still exists (verified on 26.8.1). The business identity
--    (app_users.subject, referenced by every project, approval and audit row) never
--    changes; a link maps (issuer, Keycloak sub) to it. Under the dedicated realm
--    only linked identities can sign in; the shared realm keeps sub = subject, so a
--    switch back needs no data change.
CREATE TABLE identity_links (
 issuer text NOT NULL,
 external_subject text NOT NULL CHECK (external_subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'),
 subject text NOT NULL REFERENCES app_users(subject) ON UPDATE CASCADE,
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (issuer, external_subject),
 UNIQUE (issuer, subject)
);

-- 3. Account service. Login phone mirrors the Keycloak username (11 digits for
--    mainland numbers); account_state tracks the Keycloak side so the page never
--    shows "enabled" for an account that Keycloak has not enabled.
ALTER TABLE app_users
 ADD COLUMN account_state text NOT NULL DEFAULT 'NONE'
  CHECK (account_state IN ('NONE','CREATING','READY','DISABLED','FAILED')),
 ADD COLUMN account_must_change_password boolean NOT NULL DEFAULT false;

-- One row per requested account operation. The idempotency key makes a retried
-- request (double click, timeout) return the first result instead of acting twice.
-- No password, temporary or otherwise, is ever stored here or anywhere else.
CREATE TABLE account_operations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 idempotency_key text NOT NULL UNIQUE CHECK (idempotency_key ~ '^[A-Za-z0-9_-]{16,128}$'),
 staff_id uuid NOT NULL REFERENCES app_users(staff_id),
 operation text NOT NULL CHECK (operation IN ('CREATE','RESET_PASSWORD','CHANGE_PHONE','DISABLE','ENABLE','SET_ROLE')),
 actor_subject text NOT NULL,
 state text NOT NULL DEFAULT 'STARTED' CHECK (state IN ('STARTED','DONE','FAILED','COMPENSATED')),
 keycloak_user_id text,
 detail text NOT NULL DEFAULT '' CHECK (length(detail) <= 600),
 created_at timestamptz NOT NULL DEFAULT now(),
 finished_at timestamptz
);
CREATE INDEX account_operations_staff ON account_operations(staff_id, created_at DESC);
-- At most one unfinished operation per member: concurrent edits of the same
-- account (for example two phone changes) cannot both proceed.
CREATE UNIQUE INDEX account_operations_one_open ON account_operations(staff_id) WHERE state = 'STARTED';

-- 4. Business subjects are permanent once bound. The account service may only bind
--    a placeholder ('pending:<staff_id>') to a Keycloak UUID, or undo its own failed
--    binding (UUID back to the member's placeholder) while the account is CREATING.
--    Any other change of a bound subject is refused for every role.
CREATE FUNCTION staff_subject_guard() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF NEW.subject IS DISTINCT FROM OLD.subject THEN
  IF OLD.subject = 'pending:' || OLD.staff_id::text
     AND NEW.subject ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$' THEN
   RETURN NEW;
  END IF;
  IF NEW.subject = 'pending:' || OLD.staff_id::text AND OLD.account_state = 'CREATING' THEN
   RETURN NEW;
  END IF;
  RAISE EXCEPTION 'bound business subject is immutable' USING ERRCODE = 'BB002';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER staff_subject_guard BEFORE UPDATE OF subject ON app_users
 FOR EACH ROW EXECUTE FUNCTION staff_subject_guard();
REVOKE EXECUTE ON FUNCTION staff_subject_guard() FROM PUBLIC;
