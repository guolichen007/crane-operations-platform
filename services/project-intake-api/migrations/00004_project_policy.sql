ALTER TABLE intake_projects
 ALTER COLUMN owner_subject DROP NOT NULL,
 ADD COLUMN draft_custodian_subject text REFERENCES app_users,
 ADD COLUMN submitted_by_subject text REFERENCES app_users,
 ADD COLUMN submission_revision integer NOT NULL DEFAULT 0 CHECK(submission_revision>=0),
 ADD COLUMN evidence_state text NOT NULL DEFAULT 'INCOMPLETE' CHECK(evidence_state IN ('COMPLETE','INCOMPLETE')),
 ADD COLUMN protection_started_at timestamptz,
 ADD COLUMN protection_basis text NOT NULL DEFAULT 'LEGACY_CREATED_AT' CHECK(protection_basis IN ('LEGACY_CREATED_AT','FIRST_SUBMISSION')),
 ADD COLUMN document_hash text,
 ADD COLUMN next_action text NOT NULL DEFAULT '',
 ADD COLUMN next_action_evidence text NOT NULL DEFAULT 'COMPLETE' CHECK(next_action_evidence IN ('COMPLETE','INCOMPLETE'));
UPDATE intake_projects SET draft_custodian_subject=created_by_subject,document_hash=encode(sha256(convert_to(document::text,'UTF8')),'hex');
ALTER TABLE intake_projects ALTER COLUMN document_hash SET NOT NULL;
ALTER TABLE intake_projects ADD CHECK(document_hash ~ '^[0-9a-f]{64}$');
CREATE FUNCTION project_document_hash() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN NEW.document_hash:=encode(sha256(convert_to(NEW.document::text,'UTF8')),'hex'); RETURN NEW; END $$;
CREATE TRIGGER project_document_hash BEFORE INSERT OR UPDATE OF document ON intake_projects FOR EACH ROW EXECUTE FUNCTION project_document_hash();
REVOKE EXECUTE ON FUNCTION project_document_hash() FROM PUBLIC;
ALTER TABLE intake_projects ALTER COLUMN draft_custodian_subject SET NOT NULL;
ALTER TABLE intake_projects ADD CONSTRAINT project_unassigned_draft CHECK (
 owner_subject IS NOT NULL OR (intake_status='DRAFT' AND team_id IS NULL AND protection_status='NOT_STARTED' AND protected_until IS NULL AND next_review_at IS NULL AND protection_started_at IS NULL)
);
UPDATE intake_projects p SET next_action=coalesce((SELECT a.next_action FROM project_activities a
 WHERE a.project_id=p.id ORDER BY a.created_at DESC,a.id LIMIT 1),'');
UPDATE intake_projects p SET next_action='',next_action_evidence='INCOMPLETE' WHERE EXISTS (
 SELECT 1 FROM project_activities a JOIN project_activities b ON a.project_id=b.project_id
 AND a.created_at=b.created_at AND a.next_action<>b.next_action
 WHERE a.project_id=p.id AND a.created_at=(SELECT max(created_at) FROM project_activities WHERE project_id=p.id)
);
CREATE INDEX project_unassigned_idx ON intake_projects(draft_custodian_subject,updated_at DESC) WHERE owner_subject IS NULL;

CREATE TABLE project_submissions (
 project_id uuid NOT NULL REFERENCES intake_projects,
 revision integer NOT NULL CHECK(revision>0),
 source_version integer NOT NULL CHECK(source_version>0),
 actor_subject text NOT NULL REFERENCES app_users,
 creator_subject text NOT NULL REFERENCES app_users,
 owner_subject text NOT NULL REFERENCES app_users,
 team_id uuid NOT NULL REFERENCES teams,
 document jsonb NOT NULL,
 document_hash text NOT NULL CHECK(document_hash ~ '^[0-9a-f]{64}$'),
 participants jsonb NOT NULL CHECK(jsonb_typeof(participants)='array'),
 evidence_state text NOT NULL CHECK(evidence_state IN ('COMPLETE','INCOMPLETE')),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(project_id,revision)
);
CREATE TABLE project_participations (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
 project_id uuid NOT NULL REFERENCES intake_projects,
 actor_subject text NOT NULL REFERENCES app_users,
 document_hash text NOT NULL CHECK(document_hash ~ '^[0-9a-f]{64}$'),
 kind text NOT NULL CHECK(kind IN ('CREATE','EDIT','ASSIGN','STAGE')),
 source_version integer NOT NULL CHECK(source_version>0),
 owner_subject text REFERENCES app_users,
 team_id uuid REFERENCES teams,
 created_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(project_id,source_version,actor_subject,kind)
);
CREATE INDEX participation_actor_idx ON project_participations(project_id,actor_subject);
CREATE TABLE project_create_requests (
 actor_subject text NOT NULL REFERENCES app_users,
 idempotency_key text NOT NULL CHECK(length(idempotency_key) BETWEEN 16 AND 128),
 request_hash text NOT NULL CHECK(request_hash ~ '^[0-9a-f]{64}$'),
 project_id uuid NOT NULL REFERENCES intake_projects,
 created_version integer NOT NULL CHECK(created_version>0),
 created_at timestamptz NOT NULL DEFAULT now(),
 PRIMARY KEY(actor_subject,idempotency_key)
);
ALTER TABLE approvals ADD COLUMN submission_revision integer;
ALTER TABLE approvals ADD FOREIGN KEY(project_id,submission_revision) REFERENCES project_submissions(project_id,revision);
ALTER TABLE duplicate_cases ADD COLUMN submission_revision integer;
ALTER TABLE duplicate_cases ADD FOREIGN KEY(project_id,submission_revision) REFERENCES project_submissions(project_id,revision);

CREATE FUNCTION project_evidence_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'immutable project evidence' USING ERRCODE='BB002'; END $$;
CREATE TRIGGER submission_immutable BEFORE UPDATE OR DELETE ON project_submissions FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();
CREATE TRIGGER participation_immutable BEFORE UPDATE OR DELETE ON project_participations FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();
CREATE TRIGGER create_receipt_immutable BEFORE UPDATE OR DELETE ON project_create_requests FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();
CREATE FUNCTION duplicate_decision_immutable() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.decision<>'PENDING' THEN RAISE EXCEPTION 'immutable duplicate decision' USING ERRCODE='BB002'; END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER duplicate_decision_immutable BEFORE UPDATE OR DELETE ON duplicate_cases FOR EACH ROW EXECUTE FUNCTION duplicate_decision_immutable();
REVOKE EXECUTE ON FUNCTION project_evidence_immutable(),duplicate_decision_immutable() FROM PUBLIC;

ALTER TABLE intake_projects ADD CONSTRAINT submission_projection CHECK ((submission_revision=0 AND submitted_by_subject IS NULL) OR (submission_revision>0 AND submitted_by_subject IS NOT NULL AND owner_subject IS NOT NULL));
