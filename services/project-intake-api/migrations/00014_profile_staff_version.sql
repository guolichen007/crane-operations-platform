-- V1.1 R7-D2: a member's own name change also advances staff_version.
--
-- 00001-00013 are not modified. confirm_own_profile (00010) advances
-- profile_version only. For staff-managed members (everyone created through
-- staff management) nothing advanced staff_version, so a supervisor or an
-- administrator still holding the staff page from before the member's change
-- could submit the old name with a version the server still accepted, and
-- silently overwrite it. 00003's staff_legacy_state only covers members that
-- are not staff managed.
--
-- This trigger runs only for the member's own change (baobei.profile_self=1,
-- set by confirm_own_profile) and assigns OLD.staff_version + 1, exactly like
-- staff_legacy_state does, so for any row the version advances once even when
-- both triggers fire. Staff-management edits set staff_version themselves and
-- are not touched here; provisioning, activation, role sync and account
-- operations do not change display_name and are therefore unaffected.
CREATE FUNCTION profile_self_staff_version() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF NEW.display_name IS DISTINCT FROM OLD.display_name
    AND coalesce(current_setting('baobei.profile_self', true), '') = '1' THEN
  NEW.staff_version := OLD.staff_version + 1;
 END IF;
 RETURN NEW;
END $$;
REVOKE EXECUTE ON FUNCTION profile_self_staff_version() FROM PUBLIC;
CREATE TRIGGER profile_self_staff_version BEFORE UPDATE OF display_name ON app_users
 FOR EACH ROW EXECUTE FUNCTION profile_self_staff_version();
