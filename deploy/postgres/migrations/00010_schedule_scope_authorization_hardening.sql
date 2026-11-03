-- +goose Up
-- R2 B2-R2 — schedule scope authorization hardening.
-- 00001..00009 are frozen.

-- ---------------------------------------------------------------------------
-- Formal Project <-> Portfolio link. Legacy projects stay NULL; intake-approved
-- projects get the intake's portfolio scope.
-- ---------------------------------------------------------------------------
ALTER TABLE projects ADD COLUMN portfolio_scope_id text REFERENCES portfolio_scopes(id);
UPDATE projects p SET portfolio_scope_id = pi.portfolio_scope_id
FROM project_intakes pi WHERE pi.formal_project_id = p.id;

-- ---------------------------------------------------------------------------
-- Backfill schedule_items.portfolio_scope_id from the owning project, then make
-- it authoritative (NOT NULL). Any unexplained NULL fails the migration rather
-- than guessing a portfolio.
-- ---------------------------------------------------------------------------
UPDATE schedule_items s SET portfolio_scope_id = p.portfolio_scope_id
FROM projects p WHERE p.id = s.project_id AND s.portfolio_scope_id IS NULL;

-- +goose StatementBegin
DO $$ BEGIN
  IF EXISTS (SELECT 1 FROM schedule_items WHERE portfolio_scope_id IS NULL) THEN
    RAISE EXCEPTION 'schedule_items.portfolio_scope_id has unexplained NULLs';
  END IF;
END $$;
-- +goose StatementEnd

ALTER TABLE schedule_items ALTER COLUMN portfolio_scope_id SET NOT NULL;
CREATE INDEX projects_portfolio_scope ON projects(portfolio_scope_id);

-- +goose Down
-- Forward-only: scope hardening is additive over frozen B2.
-- +goose StatementBegin
DO $$ BEGIN
  RAISE EXCEPTION 'schedule scope authorization hardening is forward-only; use a verified backup restore';
END $$;
-- +goose StatementEnd
