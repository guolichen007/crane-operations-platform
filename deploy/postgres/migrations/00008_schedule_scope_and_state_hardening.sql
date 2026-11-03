-- +goose Up
-- R2 B2-R1 — schedule scope and reservation state hardening.
-- 00001..00007 are frozen. B3 asset schema moves to 00009.

-- ---------------------------------------------------------------------------
-- Tenant invariant: an intake's tenant must equal its portfolio scope's tenant.
-- ---------------------------------------------------------------------------
ALTER TABLE portfolio_scopes ADD CONSTRAINT portfolio_scopes_id_tenant_key UNIQUE (id, tenant_id);
ALTER TABLE project_intakes ADD CONSTRAINT project_intakes_scope_tenant_fk
  FOREIGN KEY (portfolio_scope_id, tenant_id) REFERENCES portfolio_scopes (id, tenant_id);

-- ---------------------------------------------------------------------------
-- Cross-portfolio DB guard. schedule_items and resources both gain a composite
-- key (id, portfolio_scope_id); reservations carry portfolio_scope_id and are
-- pinned to both ends via composite FKs, so an item can never reserve a
-- resource of another portfolio.
-- ---------------------------------------------------------------------------
ALTER TABLE schedule_items ADD COLUMN portfolio_scope_id text;
ALTER TABLE resources ADD CONSTRAINT resources_id_scope_key UNIQUE (id, portfolio_scope_id);
ALTER TABLE schedule_items ADD CONSTRAINT schedule_items_id_scope_key UNIQUE (id, portfolio_scope_id);

ALTER TABLE resource_reservations ADD COLUMN portfolio_scope_id text;
UPDATE resource_reservations r SET portfolio_scope_id = s.portfolio_scope_id
FROM schedule_items s WHERE s.id = r.schedule_item_id;
UPDATE schedule_items s SET portfolio_scope_id = pi.portfolio_scope_id
FROM project_intakes pi WHERE pi.formal_project_id = s.project_id;

ALTER TABLE resource_reservations ADD CONSTRAINT resource_reservations_item_scope_fk
  FOREIGN KEY (schedule_item_id, portfolio_scope_id) REFERENCES schedule_items (id, portfolio_scope_id);
ALTER TABLE resource_reservations ADD CONSTRAINT resource_reservations_resource_scope_fk
  FOREIGN KEY (resource_id, portfolio_scope_id) REFERENCES resources (id, portfolio_scope_id);

-- ---------------------------------------------------------------------------
-- Reservation lifecycle: PENDING (draft, invisible to competitors) -> ACTIVE
-- (on approval) / CANCELLED. Two concurrent approvals can no longer both see
-- each other's unapproved reservation as ACTIVE.
-- ---------------------------------------------------------------------------
ALTER TABLE resource_reservations DROP CONSTRAINT resource_reservations_status_check;
ALTER TABLE resource_reservations ADD CONSTRAINT resource_reservations_status_check CHECK (status IN ('PENDING','ACTIVE','CANCELLED'));
ALTER TABLE resource_reservations ALTER COLUMN status SET DEFAULT 'PENDING';

CREATE INDEX schedule_items_portfolio ON schedule_items(portfolio_scope_id);
CREATE INDEX resource_reservations_portfolio ON resource_reservations(portfolio_scope_id);

-- +goose Down
-- Forward-only: schedule scope/state hardening is additive over frozen B2.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'schedule scope/state hardening migration is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
