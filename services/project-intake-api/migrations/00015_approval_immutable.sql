-- V1.1 final quality closure: approval history is immutable evidence (PRD 9.3).
--
-- 00001-00014 are not modified. Every business and maintenance path only
-- INSERTs approvals (governance.go decision transaction); approve, return,
-- reject, resubmission, re-review, closing and transfer all add new rows and
-- never rewrite an earlier one, and no foreign key cascades into approvals.
-- The runtime role already has only SELECT/INSERT; this trigger additionally
-- refuses UPDATE, DELETE and TRUNCATE by any ordinary role (migrator,
-- maintenance, mistakes in a psql session), reusing the existing evidence
-- function. It is not a defence against a superuser who disables triggers.
CREATE TRIGGER approval_immutable BEFORE UPDATE OR DELETE ON approvals
 FOR EACH ROW EXECUTE FUNCTION project_evidence_immutable();
CREATE TRIGGER approval_no_truncate BEFORE TRUNCATE ON approvals
 FOR EACH STATEMENT EXECUTE FUNCTION project_evidence_immutable();
