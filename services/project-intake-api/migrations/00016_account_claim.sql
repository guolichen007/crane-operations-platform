-- V1.1 production go-live: first-time account claim (首次认领).
--
-- 00001-00015 are not modified. An administrator may open a login account
-- under a unique temporary login name (claim_login, e.g. sales01) instead of
-- the work phone. The member signs in with it, sets a password (Keycloak
-- temporary password), then must enter their real name and real mainland
-- mobile number. The account service changes the Keycloak username to the
-- phone (the temporary name stops working), stores the phone and the name,
-- ends every session and the member signs in again with the phone. Until the
-- claim is complete the API serves nothing but the claim itself.
--
-- The staff id, subject, identity link, role and team never change here. The
-- claim is an ordinary account operation (CLAIM): idempotency key, one open
-- operation per member, one open operation per target login, lease, recovery
-- and REVIEW exactly like the other operations.

-- 1. Members: temporary login name and claim state. A temporary name is never
--    11 digits (it starts with a letter), so it can never equal a phone login.
ALTER TABLE app_users
 ADD COLUMN claim_login text CHECK (claim_login IS NULL OR claim_login ~ '^[a-z][a-z0-9_-]{2,31}$'),
 ADD COLUMN claim_state text NOT NULL DEFAULT 'NONE' CHECK (claim_state IN ('NONE','PENDING','DONE')),
 ADD CONSTRAINT claim_login_pending CHECK ((claim_state = 'PENDING') = (claim_login IS NOT NULL)),
 -- A member waiting for the claim has no work phone yet; after it, the phone is required.
 ADD CONSTRAINT claim_phone CHECK (claim_state <> 'PENDING' OR work_phone IS NULL);
CREATE UNIQUE INDEX claim_login_unique ON app_users(claim_login) WHERE claim_login IS NOT NULL;

-- 2. Account operations: CLAIM; logins may be a temporary name; the requested
--    display name travels with the operation so recovery can finish it.
ALTER TABLE account_operations DROP CONSTRAINT account_operations_operation_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_operation_check
 CHECK (operation IN ('CREATE','RESET_PASSWORD','CHANGE_PHONE','DISABLE','ENABLE','SET_ROLE','REVOKE','CLAIM'));
ALTER TABLE account_operations DROP CONSTRAINT account_operations_target_login_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_target_login_check
 CHECK (target_login IS NULL OR target_login ~ '^1[3-9][0-9]{9}$' OR target_login ~ '^[a-z][a-z0-9_-]{2,31}$');
ALTER TABLE account_operations DROP CONSTRAINT account_operations_previous_login_check;
ALTER TABLE account_operations ADD CONSTRAINT account_operations_previous_login_check
 CHECK (previous_login IS NULL OR previous_login ~ '^1[3-9][0-9]{9}$' OR previous_login ~ '^[a-z][a-z0-9_-]{2,31}$');
ALTER TABLE account_operations
 ADD COLUMN claim_name text CHECK (claim_name IS NULL OR length(claim_name) BETWEEN 1 AND 64),
 ADD CONSTRAINT account_operations_claim CHECK ((operation = 'CLAIM') = (claim_name IS NOT NULL));

-- 3. The only way a claim changes the display name: for a member whose claim is
--    pending and who has exactly this open CLAIM operation carrying this name.
--    Same rules and audit as confirm_own_profile (00010); role, team, subject
--    and staff id are not parameters.
CREATE FUNCTION claim_own_profile(p_operation uuid) RETURNS integer
 LANGUAGE plpgsql SECURITY DEFINER SET search_path FROM CURRENT AS $$
DECLARE
 u app_users%ROWTYPE;
 n text;
BEGIN
 SELECT o.claim_name INTO n FROM account_operations o
  WHERE o.id = p_operation AND o.operation = 'CLAIM' AND o.state = 'STARTED';
 IF NOT FOUND OR n IS NULL OR n <> btrim(n) OR n ~ '^[0-9[:space:]]+$' OR n ~ '[[:cntrl:]]' THEN
  RAISE EXCEPTION 'no open claim' USING ERRCODE = 'BB016';
 END IF;
 SELECT a.* INTO u FROM app_users a JOIN account_operations o ON o.staff_id = a.staff_id
  WHERE o.id = p_operation AND a.claim_state = 'PENDING' FOR UPDATE OF a;
 IF NOT FOUND THEN
  RAISE EXCEPTION 'claim not pending' USING ERRCODE = 'BB016';
 END IF;
 PERFORM set_config('baobei.profile_self', '1', true);
 UPDATE app_users SET display_name = n, profile_completed_at = coalesce(profile_completed_at, now()),
  profile_name_source = 'SELF_CONFIRMED', profile_version = profile_version + 1
  WHERE staff_id = u.staff_id;
 PERFORM set_config('baobei.profile_self', '', true);
 INSERT INTO profile_audit(staff_id, actor_subject, action, old_name, new_name, reason, profile_version)
  VALUES (u.staff_id, u.subject, 'profile.confirm', u.display_name, n, '首次认领账号', u.profile_version + 1);
 RETURN u.profile_version + 1;
END $$;
REVOKE EXECUTE ON FUNCTION claim_own_profile(uuid) FROM PUBLIC;
