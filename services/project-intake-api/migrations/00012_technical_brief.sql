-- V1.1 R6 (B3): industrial technical requirements and clarifications.
--
-- 00001-00011 are not modified. The filed document, planned_equipment,
-- project_requirements and the frozen project_submissions stay exactly as
-- filed; technical work during technical exchange / site survey is recorded
-- next to them:
--   * project_technical_briefs: the current technical brief of a project
--     (scenario, nature, workshop/line, constraints, key parameters,
--     supplementary equipment, status), optimistic version.
--   * project_technical_brief_revisions: immutable snapshot of every saved
--     version (who, when, reason) — the requirement history.
--   * project_technical_questions + _events: clarification items with asker,
--     answerer, status and the brief version they refer to; every change is an
--     immutable event.
CREATE TABLE project_technical_briefs (
 project_id uuid PRIMARY KEY REFERENCES intake_projects,
 version integer NOT NULL CHECK (version > 0),
 status text NOT NULL CHECK (status IN ('NOT_STARTED','COLLECTING','CLARIFIED')),
 project_nature text NOT NULL DEFAULT '' CHECK (project_nature IN ('','NEW_BUILD','RETROFIT','EXPANSION','REPLACEMENT')),
 scenario text NOT NULL DEFAULT '' CHECK (length(scenario) <= 2000),
 workshop text NOT NULL DEFAULT '' CHECK (length(workshop) <= 200),
 production_line text NOT NULL DEFAULT '' CHECK (length(production_line) <= 200),
 site_constraints text NOT NULL DEFAULT '' CHECK (length(site_constraints) <= 4000),
 parameters jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(parameters) = 'array'),
 equipment jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(equipment) = 'array'),
 updated_by_subject text NOT NULL REFERENCES app_users,
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE project_technical_brief_revisions (
 project_id uuid NOT NULL REFERENCES intake_projects,
 version integer NOT NULL CHECK (version > 0),
 snapshot jsonb NOT NULL,
 actor_subject text NOT NULL REFERENCES app_users,
 reason text NOT NULL CHECK (length(reason) <= 600),
 at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY (project_id, version)
);
CREATE TRIGGER technical_revision_immutable BEFORE UPDATE OR DELETE ON project_technical_brief_revisions
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();

CREATE TABLE project_technical_questions (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 project_id uuid NOT NULL REFERENCES intake_projects,
 question text NOT NULL CHECK (length(question) BETWEEN 1 AND 2000),
 status text NOT NULL CHECK (status IN ('OPEN','ANSWERED','CLOSED')),
 answer text NOT NULL DEFAULT '' CHECK (length(answer) <= 4000),
 asked_by_subject text NOT NULL REFERENCES app_users,
 answered_by_subject text REFERENCES app_users,
 brief_version integer NOT NULL DEFAULT 0 CHECK (brief_version >= 0),
 version integer NOT NULL DEFAULT 1 CHECK (version > 0),
 created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX technical_questions_project ON project_technical_questions(project_id, created_at);
CREATE TABLE project_technical_question_events (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 question_id uuid NOT NULL REFERENCES project_technical_questions,
 project_id uuid NOT NULL REFERENCES intake_projects,
 actor_subject text NOT NULL REFERENCES app_users,
 action text NOT NULL CHECK (action IN ('question.ask','question.answer','question.close','question.reopen')),
 text text NOT NULL DEFAULT '',
 at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX technical_question_events_q ON project_technical_question_events(question_id, at);
CREATE TRIGGER technical_question_event_immutable BEFORE UPDATE OR DELETE ON project_technical_question_events
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();

-- V1.1: these technical records also make a subject "referenced" (history).
CREATE OR REPLACE FUNCTION staff_subject_referenced(s text) RETURNS boolean LANGUAGE sql STABLE
 SECURITY DEFINER SET search_path FROM CURRENT AS $$
 SELECT EXISTS(SELECT 1 FROM intake_projects WHERE owner_subject=s OR created_by_subject=s OR draft_custodian_subject=s OR submitted_by_subject=s)
  OR EXISTS(SELECT 1 FROM project_activities WHERE subject=s)
  OR EXISTS(SELECT 1 FROM approvals WHERE subject=s)
  OR EXISTS(SELECT 1 FROM ownership_history WHERE from_subject=s OR to_subject=s OR actor_subject=s)
  OR EXISTS(SELECT 1 FROM duplicate_cases WHERE decided_by_subject=s)
  OR EXISTS(SELECT 1 FROM project_submissions WHERE actor_subject=s OR creator_subject=s OR owner_subject=s OR participants ? s)
  OR EXISTS(SELECT 1 FROM project_participations WHERE actor_subject=s OR owner_subject=s)
  OR EXISTS(SELECT 1 FROM project_create_requests WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM audit_events WHERE subject=s OR position('"'||s||'"' in details::text)>0)
  OR EXISTS(SELECT 1 FROM staff_audit WHERE actor=s)
  OR EXISTS(SELECT 1 FROM account_operations WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM project_review_routes WHERE owner_subject=s)
  OR EXISTS(SELECT 1 FROM profile_audit WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM customer_audit WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM project_technical_brief_revisions WHERE actor_subject=s)
  OR EXISTS(SELECT 1 FROM project_technical_question_events WHERE actor_subject=s)
$$;
REVOKE EXECUTE ON FUNCTION staff_subject_referenced(text) FROM PUBLIC;
