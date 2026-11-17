-- V1.1 R1: supervisors may file their own projects; such filings are reviewed
-- by a sales director, never by a supervisor.
--
-- 00001-00008 are not modified. Each submitted revision gets one immutable
-- review-route record written in the submission transaction: who owned the
-- project and in which role at that moment, and therefore which level reviews
-- it. The route of a revision never becomes lower afterwards (promotion,
-- handoff or transfer during review cannot move a director-routed filing back
-- to a supervisor); the application additionally raises the effective route to
-- DIRECTOR whenever the current owner is not a sales member.
CREATE TABLE project_review_routes (
 project_id uuid NOT NULL,
 submission_revision integer NOT NULL CHECK (submission_revision > 0),
 owner_subject text NOT NULL REFERENCES app_users,
 owner_role text NOT NULL CHECK (owner_role IN ('baobei_sales','baobei_manager','baobei_director','baobei_admin')),
 team_id uuid NOT NULL REFERENCES teams,
 route text NOT NULL CHECK (route IN ('TEAM_MANAGER','DIRECTOR')),
 basis text NOT NULL CHECK (basis IN ('SUBMISSION','BACKFILL_00009')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (project_id, submission_revision),
 FOREIGN KEY (project_id, submission_revision) REFERENCES project_submissions(project_id, revision),
 CONSTRAINT review_route_by_owner_role CHECK (route = CASE WHEN owner_role = 'baobei_sales' THEN 'TEAM_MANAGER' ELSE 'DIRECTOR' END)
);
CREATE TRIGGER review_route_immutable BEFORE UPDATE OR DELETE ON project_review_routes
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();

-- Every revision frozen before V1.1 was filed by a sales owner: until 00009 only
-- sales members (or an administrator assigning a sales member) could own a
-- project, and transfers only targeted sales members. The frozen submission
-- rows themselves are read, never changed.
INSERT INTO project_review_routes(project_id, submission_revision, owner_subject, owner_role, team_id, route, basis, created_at)
 SELECT s.project_id, s.revision, s.owner_subject, 'baobei_sales', s.team_id, 'TEAM_MANAGER', 'BACKFILL_00009', s.created_at
 FROM project_submissions s;
