-- G4-S4-C: work phone for staff. New members must provide one (enforced by the API);
-- existing members may keep NULL and are shown as "待补全".
-- Stored canonical E.164 (+ and 8-15 digits); mainland mobiles as +861[3-9]xxxxxxxxx.
ALTER TABLE app_users ADD COLUMN work_phone text;
ALTER TABLE app_users ADD CONSTRAINT staff_phone_canonical CHECK (
 work_phone IS NULL OR (work_phone ~ '^\+[1-9][0-9]{7,14}$'
   AND (work_phone NOT LIKE '+86%' OR work_phone ~ '^\+861[3-9][0-9]{9}$'))
);
CREATE UNIQUE INDEX staff_phone_unique ON app_users(work_phone) WHERE work_phone IS NOT NULL;
