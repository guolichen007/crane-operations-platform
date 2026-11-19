-- +goose Up
-- R9 A1 — controlled personnel archive ("删除") and test-account purge. Additive over 00001..00013.
--
--   * users.archived_at / archived_by / archive_reason: an archived person is a permanently
--     disabled, grant-less, hidden-by-default person. The row, its display name and every audit /
--     revision / schedule / intake reference stay intact, so historical responsibility never breaks.
--     An archived person can never be re-enabled (CHECK below + service guard).
--   * users.protected_reason: OWNER / service identities that must never be archived or purged
--     (set only by the host recovery tool, audited).
--   * account_provisions.state gains PURGED: a purged test account's opening record is kept as a
--     tombstone (request scrubbed of personal data). It keeps the username RESERVED (it stays in
--     the live-username index), because the local user id equals the username and the retained
--     audit history of the purged id must never be attributed to a new person.
ALTER TABLE users
  ADD COLUMN archived_at timestamptz,
  ADD COLUMN archived_by text,
  ADD COLUMN archive_reason text CHECK (archive_reason IS NULL OR length(archive_reason) <= 200),
  ADD COLUMN protected_reason text CHECK (protected_reason IS NULL OR protected_reason IN ('OWNER','SERVICE','SYSTEM'));
ALTER TABLE users ADD CONSTRAINT users_archived_disabled CHECK (archived_at IS NULL OR NOT enabled);
ALTER TABLE users ADD CONSTRAINT users_archived_unprotected CHECK (archived_at IS NULL OR protected_reason IS NULL);
CREATE INDEX users_archived ON users (archived_at) WHERE archived_at IS NOT NULL;

ALTER TABLE account_provisions DROP CONSTRAINT account_provisions_state_check;
ALTER TABLE account_provisions ADD CONSTRAINT account_provisions_state_check
  CHECK (state IN ('STARTED','IDP_CREATED','BOUND','COMPLETED','FAILED','CANCELLED','PURGED'));

-- A purged username stays reserved even when the account was never opened through the web
-- (adopted / host-bound identities have no provision row): the purge writes this tombstone.
CREATE TABLE purged_usernames (
  username text PRIMARY KEY,
  user_id text NOT NULL,
  purged_at timestamptz NOT NULL DEFAULT now(),
  purged_by text NOT NULL
);

-- +goose Down
-- Forward-only: archive state and protection markers are part of the security history.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'personnel archive migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
