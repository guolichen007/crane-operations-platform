-- V1.1 R7-D: one version for every display-name change.
--
-- 00001-00012 are not modified. Until now a name changed through staff
-- management or the identity manifest advanced staff_version but not
-- profile_version, so a member could submit an own-name change based on a
-- stale profile page and silently overwrite the correction. From here on any
-- change of display_name that does not come from confirm_own_profile (which
-- advances profile_version itself, exactly once) advances profile_version in
-- the same row update; the 00010 audit trigger then records the new version.
CREATE FUNCTION profile_name_version() RETURNS trigger LANGUAGE plpgsql
 SET search_path FROM CURRENT AS $$
BEGIN
 IF NEW.display_name IS DISTINCT FROM OLD.display_name
    AND coalesce(current_setting('baobei.profile_self', true), '') <> '1' THEN
  NEW.profile_version := OLD.profile_version + 1;
 END IF;
 RETURN NEW;
END $$;
REVOKE EXECUTE ON FUNCTION profile_name_version() FROM PUBLIC;
CREATE TRIGGER profile_name_version BEFORE UPDATE OF display_name ON app_users
 FOR EACH ROW EXECUTE FUNCTION profile_name_version();
