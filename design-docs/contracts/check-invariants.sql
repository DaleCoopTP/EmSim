-- Только на схеме, созданной check.py в отдельной пустой БД. Все fixtures откатываются.
BEGIN;

INSERT INTO users(id,login,password_hash,full_name,role) VALUES
('00000000-0000-4000-8000-000000000001','test_teacher','fixture','Teacher','instructor'),
('00000000-0000-4000-8000-000000000002','test_trainee','fixture','Trainee','trainee');
INSERT INTO workstations(id,number) VALUES('00000000-0000-4000-8000-000000000003',999);
INSERT INTO services(code,name,workflow) VALUES('test','Test','{}');
INSERT INTO scenarios(id,title,target_service,difficulty,origin,status,created_by) VALUES
('00000000-0000-4000-8000-000000000004','Fixture','test',4,'manual','approved','00000000-0000-4000-8000-000000000001');
INSERT INTO scenario_versions(id,scenario_id,version,status,body,digest,difficulty,created_by,approved_by,approved_at) VALUES
('00000000-0000-4000-8000-000000000005','00000000-0000-4000-8000-000000000004',1,'approved','{}',decode(repeat('00',32),'hex'),4,'00000000-0000-4000-8000-000000000001','00000000-0000-4000-8000-000000000001',now());
INSERT INTO lessons(id,instructor_id,title,mode,level,state,timing,rubric_version,started_at) VALUES
('00000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000001','Fixture','training','easy','running','{"open_s":30,"primary_s":30,"complete_s":180}','dds/rubric-v1',now());
INSERT INTO runs(id,lesson_id,user_id,workstation_id,state,level_at_start) VALUES
('00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000006','00000000-0000-4000-8000-000000000002','00000000-0000-4000-8000-000000000003','finished','easy');
INSERT INTO items(id,run_id,scenario_version_id,ordinal,state,reaction,timing_effective,opened_at,primary_at,closed_at,close_reason) VALUES
('00000000-0000-4000-8000-000000000008','00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000005',1,'closed','completed','{}',now(),now(),now(),'completed');
INSERT INTO evidence(item_id,body,digest) VALUES
('00000000-0000-4000-8000-000000000008','{"derived":{"open_seconds":10,"primary_seconds":20,"work_seconds":100,"total_seconds":120}}',decode(repeat('00',32),'hex'));
INSERT INTO assessment_inputs(id,item_id,body,digest) VALUES
('00000000-0000-4000-8000-000000000009','00000000-0000-4000-8000-000000000008','{}',decode(repeat('00',32),'hex'));
INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,score,passed,criteria) VALUES
('00000000-0000-4000-8000-000000000011','00000000-0000-4000-8000-000000000008',1,'auto','ready',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000009','00000000-0000-4000-8000-000000000021','dds/rubric-v1','{}',60,false,'[]');
INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,base_revision,rubric_version,rubric_effective,score,passed,criteria,created_by,reason) VALUES
('00000000-0000-4000-8000-000000000012','00000000-0000-4000-8000-000000000008',2,'expert','ready',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000009',1,'dds/rubric-v1','{}',85,true,'[]','00000000-0000-4000-8000-000000000001','Correction'),
('00000000-0000-4000-8000-000000000013','00000000-0000-4000-8000-000000000008',3,'expert','ready',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000009',2,'dds/rubric-v1','{}',90,true,'[]','00000000-0000-4000-8000-000000000001','Correction again');

-- Изменение актуального профиля/сценария не меняет историю.
UPDATE scenarios SET difficulty=9 WHERE id='00000000-0000-4000-8000-000000000004';
UPDATE users SET level='hard' WHERE id='00000000-0000-4000-8000-000000000002';

INSERT INTO actions(item_id,seq,log_seq,id,actor_id,request_digest,command_id,type,accepted,rejection,receipt,http_status) VALUES
('00000000-0000-4000-8000-000000000008',1,1,'00000000-0000-4000-8000-000000000030','00000000-0000-4000-8000-000000000002',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000031','open',true,NULL,'{}',200),
('00000000-0000-4000-8000-000000000008',1,2,'00000000-0000-4000-8000-000000000032','00000000-0000-4000-8000-000000000002',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000033','set_status',false,'item_closed','{}',409);
INSERT INTO tasks(id,kind,scope_type,dedup_key,status,max_attempts,wait_until,wait_reason) VALUES
('00000000-0000-4000-8000-000000000040','assessment.evaluate','item','fixture:wait','waiting',3,now()+interval '3 minutes','recording_pending');

DO $$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM item_final_assessment WHERE revision=3 AND kind='expert' AND score=90) THEN
    RAISE EXCEPTION 'latest expert must be final';
  END IF;
  IF NOT EXISTS(SELECT 1 FROM lesson_report_rows WHERE difficulty=4 AND level='easy' AND work_seconds=100 AND total_seconds=120) THEN
    RAISE EXCEPTION 'historical report changed with live profile or scenario';
  END IF;
  IF (SELECT count(*) FROM actions WHERE seq=1) <> 2 THEN RAISE EXCEPTION 'rejected attempt needs independent log identity'; END IF;
  BEGIN
    UPDATE scenario_versions SET difficulty=9;
    RAISE EXCEPTION 'historical scenario difficulty was mutable';
  EXCEPTION WHEN raise_exception THEN
    IF SQLERRM <> 'immutable scenario version content' THEN RAISE; END IF;
  END;
  BEGIN
    INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,score,passed,criteria) VALUES
    ('00000000-0000-4000-8000-000000000014','00000000-0000-4000-8000-000000000008',4,'auto','ready',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000009','00000000-0000-4000-8000-000000000022','dds/rubric-v1','{}',20,false,'[]');
    RAISE EXCEPTION 'second auto was accepted';
  EXCEPTION WHEN check_violation OR unique_violation THEN NULL;
  WHEN raise_exception THEN IF SQLERRM <> 'auto assessment forbidden after expert' THEN RAISE; END IF;
  END;
  BEGIN
    INSERT INTO item_events(id,item_id,event_key,anchor_at,due_at,state,delivered_at,skip_reason) VALUES
    ('00000000-0000-4000-8000-000000000015','00000000-0000-4000-8000-000000000008','e4',now(),now(),'delivered',NULL,'item_closed');
    RAISE EXCEPTION 'invalid delivered event was accepted';
  EXCEPTION WHEN check_violation THEN NULL;
  END;
  BEGIN
    UPDATE tasks SET attempts=1,lease_token=1 WHERE dedup_key='fixture:wait';
    RAISE EXCEPTION 'waiting consumed an attempt';
  EXCEPTION WHEN check_violation THEN NULL;
  END;
  BEGIN
    UPDATE assessment_inputs SET body='{"changed":true}';
    RAISE EXCEPTION 'sealed input was mutable';
  EXCEPTION WHEN raise_exception THEN
    IF SQLERRM NOT LIKE 'immutable artifact:%' THEN RAISE; END IF;
  END;
  BEGIN
    UPDATE assessments SET score=0 WHERE kind='expert';
    RAISE EXCEPTION 'expert revision was mutable';
  EXCEPTION WHEN raise_exception THEN
    IF SQLERRM NOT LIKE 'immutable artifact:%' THEN RAISE; END IF;
  END;
  BEGIN
    UPDATE evidence SET body='{}';
    RAISE EXCEPTION 'evidence was mutable';
  EXCEPTION WHEN raise_exception THEN
    IF SQLERRM NOT LIKE 'immutable artifact:%' THEN RAISE; END IF;
  END;
END;
$$;

-- Первая ручная оценка не требует auto или подготовленного input.
INSERT INTO items(id,run_id,scenario_version_id,ordinal,state,reaction,timing_effective,opened_at,primary_at,closed_at,close_reason,interruptions,stop_cutoff_log_seq) VALUES
('00000000-0000-4000-8000-000000000050','00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000005',2,'closed','completed','{}',now(),now(),now(),'completed','[{"cause":"server_restart"}]',3);
INSERT INTO evidence(item_id,body,digest) VALUES
('00000000-0000-4000-8000-000000000050','{}',decode(repeat('00',32),'hex'));
INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,base_revision,rubric_version,rubric_effective,score,passed,criteria,created_by,reason) VALUES
('00000000-0000-4000-8000-000000000051','00000000-0000-4000-8000-000000000050',2,'expert','ready',decode(repeat('00',32),'hex'),0,'dds/rubric-v1','{}',80,true,'[]','00000000-0000-4000-8000-000000000001','Manual without auto');
INSERT INTO trainee_assessment_state(user_id,exercise_type,version,advice_due_at) VALUES
('00000000-0000-4000-8000-000000000002','dds_processing',1,now());
UPDATE tasks SET status='cancelled',terminal_at=now(),wait_until=NULL,wait_reason=NULL WHERE dedup_key='fixture:wait';
DO $$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM item_final_assessment WHERE item_id='00000000-0000-4000-8000-000000000050' AND kind='expert' AND revision=2) THEN
    RAISE EXCEPTION 'manual grade without auto unavailable';
  END IF;
  BEGIN
    INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,base_revision,rubric_version,rubric_effective,score,passed,criteria,created_by,reason) VALUES
    ('00000000-0000-4000-8000-000000000052','00000000-0000-4000-8000-000000000050',2,'expert','ready',decode(repeat('00',32),'hex'),0,'dds/rubric-v1','{}',80,true,'[]','00000000-0000-4000-8000-000000000001','Stale');
    RAISE EXCEPTION 'stale expert accepted';
  EXCEPTION WHEN raise_exception THEN
    IF SQLERRM <> 'stale assessment revision' THEN RAISE; END IF;
  END;
END;
$$;
-- Третья карточка: неполная автооценка без выдуманного числового балла.
INSERT INTO items(id,run_id,scenario_version_id,ordinal,state,reaction,timing_effective,opened_at,primary_at,closed_at,close_reason) VALUES
('00000000-0000-4000-8000-000000000060','00000000-0000-4000-8000-000000000007','00000000-0000-4000-8000-000000000005',3,'closed','completed','{}',now(),now(),now(),'completed');
INSERT INTO evidence(item_id,body,digest) VALUES ('00000000-0000-4000-8000-000000000060','{}',decode(repeat('00',32),'hex'));
INSERT INTO assessment_inputs(id,item_id,body,digest) VALUES ('00000000-0000-4000-8000-000000000061','00000000-0000-4000-8000-000000000060','{}',decode(repeat('00',32),'hex'));
INSERT INTO assessments(id,item_id,revision,kind,status,evidence_digest,input_id,source_task_id,rubric_version,rubric_effective,criteria) VALUES
('00000000-0000-4000-8000-000000000062','00000000-0000-4000-8000-000000000060',1,'auto','needs_review',decode(repeat('00',32),'hex'),'00000000-0000-4000-8000-000000000061','00000000-0000-4000-8000-000000000063','dds/rubric-v1','{}','[]');
DO $$
BEGIN
  IF NOT EXISTS(SELECT 1 FROM item_final_assessment WHERE item_id='00000000-0000-4000-8000-000000000060' AND status='needs_review' AND score IS NULL AND passed IS NULL) THEN
    RAISE EXCEPTION 'needs_review requires nullable result';
  END IF;
END;
$$;

ROLLBACK;
