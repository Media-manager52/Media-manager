-- Read-only request snapshot; usable on schema 40 or 41. No provider I/O.
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT now() AS measured_at;
SELECT s.request_id,s.title,s.state AS seerr_state,s.requested_at,w.id AS workflow_id,w.state,w.attention_code,w.attention_detail,p.name AS profile,r.destination_root,
(SELECT count(*) FROM release_candidates c WHERE c.workflow_id=w.id) AS candidates,
(SELECT min(e.available_at) FROM outbox_events e WHERE e.aggregate_id=w.id AND e.completed_at IS NULL) AS next_task
FROM seerr_request_snapshots s LEFT JOIN seerr_request_workflows sw ON sw.seerr_instance_id=s.seerr_instance_id AND sw.request_id=s.request_id LEFT JOIN workflows w ON w.id=sw.workflow_id LEFT JOIN quality_profiles p ON p.id=w.quality_profile_id LEFT JOIN library_roots r ON r.id=w.root_id
WHERE s.request_id IN ('422','423','424','425','426','427','428') ORDER BY s.request_id,w.variant;
SELECT request_id,source,received_at,processed_at,outcome,error_detail FROM seerr_deliveries WHERE request_id IN ('422','423','424','425','426','427','428') ORDER BY request_id,received_at;
SELECT sw.request_id,j.id AS job_id,j.state,j.acquisition_outcome,c.title AS release,sv.status AS subtitles,sv.required_languages,sv.observed_languages,
(SELECT count(*) FROM library_link_versions v WHERE v.altmount_job_id=j.id AND v.state='current') AS current_links,
(SELECT count(*) FROM plex_confirmations pc JOIN library_link_versions v ON v.id=pc.link_version_id WHERE v.altmount_job_id=j.id) AS plex_confirmations
FROM seerr_request_workflows sw JOIN altmount_jobs j ON j.workflow_id=sw.workflow_id JOIN release_candidates c ON c.id=j.candidate_id LEFT JOIN subtitle_verifications sv ON sv.altmount_job_id=j.id
WHERE sw.request_id IN ('423','424','425','426','427','428') ORDER BY sw.request_id,j.submission_started_at;
COMMIT;

BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SELECT now() AS measured_at;
SELECT w.id,w.state,w.attention_code,c.title AS selected_release,i.name AS provider
FROM workflows w JOIN release_candidates c ON c.id=w.selected_release_id JOIN integration_instances i ON i.id=c.indexer_instance_id WHERE w.id='01a0e9dc-005e-754e-adc2-cdaa4e0cdc44';
SELECT i.name AS provider,c.title AS release,e.failure_kind,e.reason,e.created_at
FROM workflow_release_exclusions e JOIN release_candidates c ON c.id=e.candidate_id JOIN integration_instances i ON i.id=e.indexer_instance_id WHERE e.workflow_id='01a0e9dc-005e-754e-adc2-cdaa4e0cdc44' ORDER BY e.created_at;
SELECT r.round_ordinal,r.state,r.attempt_limit_snapshot,count(j.id) AS submission_slots_used FROM acquisition_rounds r LEFT JOIN altmount_jobs j ON j.round_id=r.id WHERE r.workflow_id='01a0e9dc-005e-754e-adc2-cdaa4e0cdc44' GROUP BY r.id ORDER BY r.round_ordinal;
SELECT d.verdict,count(*) AS candidates,
count(*) FILTER (WHERE EXISTS(SELECT 1 FROM jsonb_array_elements(o.normalized_facts) f WHERE f->>'name'='subtitle_languages' AND f->'values' @> '["da"]'::jsonb)) AS explicit_da,
count(*) FILTER (WHERE a.candidate_id IS NULL AND e.candidate_id IS NULL) AS untried_unexcluded
FROM workflows w JOIN quality_profiles p ON p.id=w.quality_profile_id JOIN release_candidates c ON c.workflow_id=w.id JOIN release_observations o ON o.id=c.observation_id JOIN candidate_decisions d ON d.workflow_id=w.id AND d.candidate_id=c.id AND d.policy_version=p.policy_version
LEFT JOIN workflow_attempted_releases a ON a.workflow_id=w.id AND a.candidate_id=c.id LEFT JOIN workflow_release_exclusions e ON e.workflow_id=w.id AND e.indexer_instance_id=c.indexer_instance_id AND e.native_key=c.native_key
WHERE w.id='01a0e9dc-005e-754e-adc2-cdaa4e0cdc44' GROUP BY 1;
SELECT s.request_id,s.title,s.requested_at,w.terminal_at AS confirmed_at,round(extract(epoch FROM (w.terminal_at-s.requested_at))) AS seconds_to_confirmed
FROM seerr_request_snapshots s JOIN seerr_request_workflows sw ON sw.seerr_instance_id=s.seerr_instance_id AND sw.request_id=s.request_id JOIN workflows w ON w.id=sw.workflow_id WHERE s.request_id IN ('424','425','426','428');
SELECT m.title,c.title AS release,i.name AS provider,d.score
FROM workflows w JOIN media_items m ON m.id=w.media_id JOIN quality_profiles p ON p.id=w.quality_profile_id JOIN release_candidates c ON c.workflow_id=w.id JOIN candidate_decisions d ON d.workflow_id=w.id AND d.candidate_id=c.id AND d.policy_version=p.policy_version
JOIN integration_instances i ON i.id=c.indexer_instance_id
WHERE w.id='01a0e9dc-005e-754e-adc2-cdaa4e0cdc44' AND d.verdict='eligible'
AND NOT EXISTS(SELECT 1 FROM workflow_attempted_releases a WHERE a.workflow_id=w.id AND a.candidate_id=c.id)
AND NOT EXISTS(SELECT 1 FROM workflow_release_exclusions e WHERE e.workflow_id=w.id AND e.indexer_instance_id=c.indexer_instance_id AND e.native_key=c.native_key)
ORDER BY d.score DESC,c.first_seen_sequence LIMIT 5;
COMMIT;
