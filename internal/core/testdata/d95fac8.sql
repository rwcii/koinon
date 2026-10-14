PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE sessions (
			family TEXT NOT NULL, id TEXT NOT NULL, repository TEXT NOT NULL,
			directory TEXT NOT NULL, wake_target TEXT NOT NULL,
			registered_at INTEGER NOT NULL, renewed_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL, retired_at INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL, last_seq INTEGER NOT NULL DEFAULT 0, acked_through INTEGER NOT NULL DEFAULT 0, ack_mark INTEGER NOT NULL DEFAULT 0, ack_mark_at INTEGER NOT NULL DEFAULT 0, purge_at INTEGER NOT NULL DEFAULT 0, subagent INTEGER NOT NULL DEFAULT 0, role TEXT NOT NULL DEFAULT '', acked_by TEXT NOT NULL DEFAULT '', host_pid INTEGER NOT NULL DEFAULT 0, host_start INTEGER NOT NULL DEFAULT 0, tmux_socket TEXT NOT NULL DEFAULT '', tmux_pane TEXT NOT NULL DEFAULT '', succession TEXT NOT NULL DEFAULT '', read_through INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (family,id)
		);
INSERT INTO sessions VALUES('maintainer','maintainer','','','{}',0,0,9007199254740991,0,1,0,0,0,0,0,0,'','',0,0,'','','',0);
INSERT INTO sessions VALUES('codex','synthetic-role-owner','/synthetic/fixture-repo/.git','/synthetic/fixture-repo','{"launch_id":"622f9106dca7069f5629a05cb985cd8ad62deece977845c48f53f7a7c27b058b","family":"codex","directory":"/synthetic/fixture-repo","cli":"/synthetic/cli","host_pid":4242,"role":"worker"}',1800000000000,1800000000000,1800000060000,0,1,0,0,0,0,0,0,'worker','',0,0,'','','',0);
INSERT INTO sessions VALUES('claude','synthetic-participant','/synthetic/fixture-repo/.git','/synthetic/fixture-repo','{"claude_pid":4242,"cli":"/synthetic/cli","directory":"/synthetic/fixture-repo","family":"claude","host_pid":4242,"launch_id":"4835ef393545098357bf93662c9b7635c379abb5dee68fcb605ae8ad48e03ea7"}',1800000000000,1800000000000,1800000060000,0,1,0,0,0,0,0,0,'','',0,0,'','','',0);
CREATE TABLE names (
				name TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('peer','alias')),
				family TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
				repository TEXT NOT NULL DEFAULT '', holder_id TEXT NOT NULL DEFAULT ''
			, role TEXT NOT NULL DEFAULT '', conflict TEXT NOT NULL DEFAULT '');
INSERT INTO names VALUES('maintainer','peer','maintainer','maintainer','','','','');
INSERT INTO names VALUES('codex-fixture-repo-dd','peer','codex','synthetic-role-owner','/synthetic/fixture-repo/.git','','','');
INSERT INTO names VALUES('codex-fixture-repo-worker','alias','codex','','/synthetic/fixture-repo/.git','synthetic-role-owner','worker','');
INSERT INTO names VALUES('claude-fixture-repo-74','peer','claude','synthetic-participant','/synthetic/fixture-repo/.git','','','');
INSERT INTO names VALUES('claude-fixture-repo','alias','claude','','/synthetic/fixture-repo/.git','synthetic-participant','','');
CREATE TABLE messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				recipient_family TEXT NOT NULL, recipient_id TEXT NOT NULL, seq INTEGER NOT NULL,
				sender_family TEXT NOT NULL, sender_id TEXT NOT NULL, sender_name TEXT NOT NULL,
				body TEXT NOT NULL, created_at INTEGER NOT NULL,
				delivery_state TEXT NOT NULL DEFAULT 'waiting'
					CHECK (delivery_state IN ('waiting','notified','uncertain','failed')),
				delivery_reason TEXT NOT NULL DEFAULT '', delivery_updated_at INTEGER NOT NULL, wake_attempts INTEGER NOT NULL DEFAULT 0, wake_next_at INTEGER NOT NULL DEFAULT 0, wake_reason TEXT NOT NULL DEFAULT '', maintainer_ack INTEGER NOT NULL DEFAULT 0, wake_notices INTEGER NOT NULL DEFAULT 0, ack_session TEXT NOT NULL DEFAULT '', ack_via TEXT NOT NULL DEFAULT '',
				UNIQUE (recipient_family, recipient_id, seq)
			);
CREATE TABLE launches (
			id TEXT PRIMARY KEY, family TEXT NOT NULL, directory TEXT NOT NULL,
			target TEXT NOT NULL, created_at INTEGER NOT NULL
		);
INSERT INTO launches VALUES('622f9106dca7069f5629a05cb985cd8ad62deece977845c48f53f7a7c27b058b','codex','/synthetic/fixture-repo','{"family":"codex","directory":"/synthetic/fixture-repo","cli":"/synthetic/cli","host_pid":4242,"role":"worker"}',1800000000000);
INSERT INTO launches VALUES('4835ef393545098357bf93662c9b7635c379abb5dee68fcb605ae8ad48e03ea7','claude','/synthetic/fixture-repo','{"family":"claude","directory":"/synthetic/fixture-repo","cli":"/synthetic/cli","host_pid":4242}',1800000000000);
CREATE TABLE memory_stores (repository TEXT PRIMARY KEY, store_id TEXT NOT NULL,
				head INTEGER NOT NULL DEFAULT 0, floor INTEGER NOT NULL DEFAULT 0, work_counter INTEGER NOT NULL DEFAULT 0, claim_counter INTEGER NOT NULL DEFAULT 0);
INSERT INTO memory_stores VALUES('/synthetic/fixture-repo/.git','6e2f7f3cc99bd7122664e83b68cba79e',9,0,4,3);
CREATE TABLE memory_entries (repository TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				type TEXT NOT NULL, scope TEXT NOT NULL, scope_target TEXT, path TEXT, body TEXT NOT NULL,
				author TEXT, writer_family TEXT NOT NULL, writer_name TEXT NOT NULL, consumer TEXT NOT NULL,
				revision INTEGER NOT NULL, supersedes INTEGER, revokes INTEGER, superseded_by INTEGER,
				revoked_by INTEGER, conflicts_with INTEGER, expires REAL, PRIMARY KEY (repository, seq));
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',1,1800000000.0,'work-event','repo','00000000000000000000000000000001',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',2,1800000000.0,'work-event','repo','00000000000000000000000000000001',NULL,'',NULL,'codex','codex-fixture-repo-dd','native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25',2,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',3,1800000000.0,'work-event','repo','00000000000000000000000000000002',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',4,1800000000.0,'work-event','repo','00000000000000000000000000000002',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',2,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',5,1800000000.0,'work-event','repo','00000000000000000000000000000003',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',6,1800000000.0,'work-event','repo','00000000000000000000000000000003',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',2,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',7,1800000000.0,'work-event','repo','00000000000000000000000000000004',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',8,1800000000.0,'work-event','repo','00000000000000000000000000000004',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',2,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/fixture-repo/.git',9,1800000000.0,'work-event','repo','00000000000000000000000000000004',NULL,'',NULL,'claude','claude-fixture-repo-74','participant:claude-fixture-repo',3,NULL,NULL,NULL,NULL,NULL,NULL);
CREATE TABLE memory_idem (repository TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				deadline REAL NOT NULL, PRIMARY KEY (repository, consumer, key));
CREATE TABLE memory_cursors (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				issued INTEGER NOT NULL, snapshot TEXT, bootstrapped INTEGER NOT NULL, resnapshot INTEGER NOT NULL,
				updated REAL NOT NULL, actor TEXT NOT NULL DEFAULT '', PRIMARY KEY (repository, consumer));
CREATE TABLE memory_retired (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				at REAL NOT NULL, PRIMARY KEY (repository, consumer));
CREATE TABLE memory_snapshots (id TEXT PRIMARY KEY, repository TEXT NOT NULL, consumer TEXT NOT NULL,
				head INTEGER NOT NULL, created REAL NOT NULL, items INTEGER NOT NULL, issued INTEGER NOT NULL,
				acked INTEGER NOT NULL DEFAULT 0, acked_at REAL);
CREATE TABLE memory_snapshot_items (id TEXT NOT NULL, position INTEGER NOT NULL, seq INTEGER NOT NULL,
				payload TEXT NOT NULL, bytes INTEGER NOT NULL, PRIMARY KEY (id, position));
CREATE TABLE work_items (store TEXT NOT NULL, work_id TEXT NOT NULL CHECK (length(work_id)=32),
				revision INTEGER NOT NULL CHECK (revision>0),
				lifecycle TEXT NOT NULL CHECK (lifecycle IN ('open','active','blocked','finished')),
				title TEXT NOT NULL, criteria TEXT NOT NULL, non_goals TEXT NOT NULL, proposed_assignee TEXT,
				created_at REAL NOT NULL, created_consumer TEXT NOT NULL, first_start_revision INTEGER,
				scope_revision INTEGER NOT NULL CHECK (scope_revision>0),
				progress_epoch INTEGER NOT NULL DEFAULT 0 CHECK (progress_epoch>=0), last_progress_at REAL,
				progress_deadline REAL, progress TEXT NOT NULL DEFAULT '', checkpoint TEXT NOT NULL DEFAULT '',
				next_artifact TEXT NOT NULL DEFAULT '', blocker TEXT NOT NULL DEFAULT '', last_writer TEXT,
				last_generation INTEGER, last_lease_expires REAL,
				lease_expired INTEGER NOT NULL DEFAULT 0 CHECK (lease_expired IN (0,1)),
				outcome TEXT CHECK (outcome IN ('completed','withdrawn')), reason TEXT NOT NULL DEFAULT '',
				references_json TEXT NOT NULL DEFAULT '[]', finished_at REAL, expires_at REAL,
				latest_seq INTEGER NOT NULL CHECK (latest_seq>0), proposed_role TEXT NOT NULL DEFAULT '',
				CHECK ((lifecycle='finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL AND expires_at IS NOT NULL)
					OR (lifecycle<>'finished' AND outcome IS NULL AND finished_at IS NULL AND expires_at IS NULL)),
				PRIMARY KEY (store, work_id));
INSERT INTO work_items VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000001',2,'active','synthetic role task','synthetic criteria','synthetic non-goals',NULL,1800000000.0,'participant:claude-fixture-repo',2,1,1,1800000000.0,1800003600.0,'','synthetic start','synthetic artifact','','native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25',1,1800003600.0,0,NULL,'','[]',NULL,NULL,2,'{"family":"codex","role":"worker"}');
INSERT INTO work_items VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000002',2,'active','synthetic legacy claim','synthetic criteria','synthetic non-goals',NULL,1800000000.0,'participant:claude-fixture-repo',2,1,1,1800000000.0,1800003600.0,'','synthetic start','synthetic artifact','','participant:claude-fixture-repo',2,1800003600.0,0,NULL,'','[]',NULL,NULL,4,'');
INSERT INTO work_items VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000003',2,'open','synthetic edited','synthetic revised criteria','synthetic non-goals',NULL,1800000000.0,'participant:claude-fixture-repo',NULL,2,0,NULL,NULL,'','','','',NULL,NULL,NULL,0,NULL,'','[]',NULL,NULL,6,'');
INSERT INTO work_items VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000004',3,'finished','synthetic finished','synthetic criteria','synthetic non-goals',NULL,1800000000.0,'participant:claude-fixture-repo',2,1,1,1800000000.0,NULL,'','synthetic start','synthetic artifact','','participant:claude-fixture-repo',3,1800000900.0,0,'completed','','["synthetic evidence"]',1800000000.0,1802592000.0,9,'');
CREATE TABLE work_scope_revisions (store TEXT NOT NULL, work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0), ts REAL NOT NULL, consumer TEXT NOT NULL, author TEXT,
				title TEXT NOT NULL, criteria TEXT NOT NULL, non_goals TEXT NOT NULL,
				PRIMARY KEY (store, work_id, revision));
INSERT INTO work_scope_revisions VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000001',1,1800000000.0,'participant:claude-fixture-repo',NULL,'synthetic role task','synthetic criteria','synthetic non-goals');
INSERT INTO work_scope_revisions VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000002',1,1800000000.0,'participant:claude-fixture-repo',NULL,'synthetic legacy claim','synthetic criteria','synthetic non-goals');
INSERT INTO work_scope_revisions VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000003',1,1800000000.0,'participant:claude-fixture-repo',NULL,'synthetic edited','synthetic criteria','synthetic non-goals');
INSERT INTO work_scope_revisions VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000003',2,1800000000.0,'participant:claude-fixture-repo',NULL,'synthetic edited','synthetic revised criteria','synthetic non-goals');
INSERT INTO work_scope_revisions VALUES('6e2f7f3cc99bd7122664e83b68cba79e','00000000000000000000000000000004',1,1800000000.0,'participant:claude-fixture-repo',NULL,'synthetic finished','synthetic criteria','synthetic non-goals');
CREATE TABLE claim_bundles (store TEXT NOT NULL, generation INTEGER NOT NULL CHECK (generation>0),
				work_id TEXT NOT NULL, consumer TEXT NOT NULL, revision INTEGER NOT NULL CHECK (revision>0),
				issued_at REAL NOT NULL, renewed_at REAL NOT NULL, expires_at REAL NOT NULL,
				progress_epoch INTEGER NOT NULL CHECK (progress_epoch>0),
				overdue_recorded INTEGER NOT NULL DEFAULT 0 CHECK (overdue_recorded IN (0,1)),
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				overdue_credit INTEGER NOT NULL DEFAULT 1 CHECK (overdue_credit IN (0,1)),
				end_credit INTEGER NOT NULL DEFAULT 1 CHECK (end_credit IN (0,1)), native_actor TEXT NOT NULL DEFAULT '',
				PRIMARY KEY (store, generation), UNIQUE (store, work_id));
INSERT INTO claim_bundles VALUES('6e2f7f3cc99bd7122664e83b68cba79e',1,'00000000000000000000000000000001','native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25',1,1800000000.0,1800000000.0,1800003600.0,1,0,1,1,1,'eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25');
INSERT INTO claim_bundles VALUES('6e2f7f3cc99bd7122664e83b68cba79e',2,'00000000000000000000000000000002','participant:claude-fixture-repo',1,1800000000.0,1800000000.0,1800003600.0,1,0,1,1,1,'');
INSERT INTO claim_bundles VALUES('6e2f7f3cc99bd7122664e83b68cba79e',3,'00000000000000000000000000000004','participant:claude-fixture-repo',1,1800000000.0,1800000000.0,1800000900.0,1,0,0,0,0,'');
CREATE TABLE claim_resources (store TEXT NOT NULL, generation INTEGER NOT NULL,
				ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 8),
				kind TEXT NOT NULL CHECK (kind IN ('writer','path','exact')), resource TEXT NOT NULL,
				PRIMARY KEY (store, generation, ordinal));
INSERT INTO claim_resources VALUES('6e2f7f3cc99bd7122664e83b68cba79e',1,0,'writer','00000000000000000000000000000001');
INSERT INTO claim_resources VALUES('6e2f7f3cc99bd7122664e83b68cba79e',2,0,'writer','00000000000000000000000000000002');
INSERT INTO claim_resources VALUES('6e2f7f3cc99bd7122664e83b68cba79e',3,0,'writer','00000000000000000000000000000004');
CREATE TABLE work_events (store TEXT NOT NULL, seq INTEGER NOT NULL CHECK (seq>0), work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0),
				kind TEXT NOT NULL CHECK (kind IN ('created','proposed','edited','started','updated','released',
					'finished','progress-overdue','lease-expired')),
				payload TEXT NOT NULL, actor TEXT NOT NULL DEFAULT '', PRIMARY KEY (store, seq));
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',1,'00000000000000000000000000000001',1,'created','{"work_id":"00000000000000000000000000000001","revision":1,"lifecycle":"open","title":"synthetic role task","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":{"family":"codex","role":"worker"},"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":null,"scope_revision":1,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":null,"finished_at":null,"expires_at":null,"latest_seq":1,"type":"work-item","seq":1,"observed_at":1800000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false,"role_resolution":{"state":"held","address":"codex-fixture-repo-worker","holder":"codex-fixture-repo-dd"}}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',2,'00000000000000000000000000000001',2,'started','{"work_id":"00000000000000000000000000000001","revision":2,"lifecycle":"active","title":"synthetic role task","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":{"family":"codex","role":"worker"},"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":2,"scope_revision":1,"progress_epoch":1,"last_progress_at":1800000000,"progress_deadline":1800003600,"progress":"","checkpoint":"synthetic start","next_artifact":"synthetic artifact","blocker":"","last_writer":"native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25","last_generation":1,"last_lease_expires":1800003600,"lease_expired":false,"outcome":null,"reason":"","references":[],"finished_at":null,"expires_at":null,"latest_seq":2,"type":"work-item","seq":2,"observed_at":1800000000,"observed_lease_expires":1800003600,"current_claim":{"consumer":"native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25","generation":1,"revision":1,"expires_at":1800003600,"resources":[["writer","00000000000000000000000000000001"]],"ownership":"native"},"lease_valid":true,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false,"role_resolution":{"state":"held","address":"codex-fixture-repo-worker","holder":"codex-fixture-repo-dd"}}','codex:synthetic-role-owner');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',3,'00000000000000000000000000000002',1,'created','{"work_id":"00000000000000000000000000000002","revision":1,"lifecycle":"open","title":"synthetic legacy claim","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":null,"scope_revision":1,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":null,"finished_at":null,"expires_at":null,"latest_seq":3,"type":"work-item","seq":3,"observed_at":1800000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',4,'00000000000000000000000000000002',2,'started','{"work_id":"00000000000000000000000000000002","revision":2,"lifecycle":"active","title":"synthetic legacy claim","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":2,"scope_revision":1,"progress_epoch":1,"last_progress_at":1800000000,"progress_deadline":1800003600,"progress":"","checkpoint":"synthetic start","next_artifact":"synthetic artifact","blocker":"","last_writer":"participant:claude-fixture-repo","last_generation":2,"last_lease_expires":1800003600,"lease_expired":false,"outcome":null,"reason":"","references":[],"finished_at":null,"expires_at":null,"latest_seq":4,"type":"work-item","seq":4,"observed_at":1800000000,"observed_lease_expires":1800003600,"current_claim":{"consumer":"participant:claude-fixture-repo","generation":2,"revision":1,"expires_at":1800003600,"resources":[["writer","00000000000000000000000000000002"]],"ownership":"legacy"},"lease_valid":true,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',5,'00000000000000000000000000000003',1,'created','{"work_id":"00000000000000000000000000000003","revision":1,"lifecycle":"open","title":"synthetic edited","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":null,"scope_revision":1,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":null,"finished_at":null,"expires_at":null,"latest_seq":5,"type":"work-item","seq":5,"observed_at":1800000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',6,'00000000000000000000000000000003',2,'edited','{"work_id":"00000000000000000000000000000003","revision":2,"lifecycle":"open","title":"synthetic edited","criteria":"synthetic revised criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":null,"scope_revision":2,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":[],"finished_at":null,"expires_at":null,"latest_seq":6,"type":"work-item","seq":6,"observed_at":1800000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1,2],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',7,'00000000000000000000000000000004',1,'created','{"work_id":"00000000000000000000000000000004","revision":1,"lifecycle":"open","title":"synthetic finished","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":null,"scope_revision":1,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":null,"finished_at":null,"expires_at":null,"latest_seq":7,"type":"work-item","seq":7,"observed_at":1800000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',8,'00000000000000000000000000000004',2,'started','{"work_id":"00000000000000000000000000000004","revision":2,"lifecycle":"active","title":"synthetic finished","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":2,"scope_revision":1,"progress_epoch":1,"last_progress_at":1800000000,"progress_deadline":1800003600,"progress":"","checkpoint":"synthetic start","next_artifact":"synthetic artifact","blocker":"","last_writer":"participant:claude-fixture-repo","last_generation":3,"last_lease_expires":1800000900,"lease_expired":false,"outcome":null,"reason":"","references":[],"finished_at":null,"expires_at":null,"latest_seq":8,"type":"work-item","seq":8,"observed_at":1800000000,"observed_lease_expires":1800000900,"current_claim":{"consumer":"participant:claude-fixture-repo","generation":3,"revision":1,"expires_at":1800000900,"resources":[["writer","00000000000000000000000000000004"]],"ownership":"legacy"},"lease_valid":true,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
INSERT INTO work_events VALUES('6e2f7f3cc99bd7122664e83b68cba79e',9,'00000000000000000000000000000004',3,'finished','{"work_id":"00000000000000000000000000000004","revision":3,"lifecycle":"finished","title":"synthetic finished","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"proposed_role":null,"created_at":1800000000,"created_consumer":"participant:claude-fixture-repo","first_start_revision":2,"scope_revision":1,"progress_epoch":1,"last_progress_at":1800000000,"progress_deadline":null,"progress":"","checkpoint":"synthetic start","next_artifact":"synthetic artifact","blocker":"","last_writer":"participant:claude-fixture-repo","last_generation":3,"last_lease_expires":1800000900,"lease_expired":false,"outcome":"completed","reason":"","references":["synthetic evidence"],"finished_at":1800000000,"expires_at":1802592000,"latest_seq":9,"type":"work-item","seq":9,"observed_at":1800000000,"observed_lease_expires":1800000900,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}','claude:synthetic-participant');
CREATE TABLE work_replays (store TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				operation TEXT NOT NULL, scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER, ts REAL NOT NULL,
				deadline REAL NOT NULL, result TEXT NOT NULL, native_actor TEXT NOT NULL DEFAULT '', PRIMARY KEY (store, consumer, key));
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-1','work-create','go1','3616d726777d219843631df35439ed430a4f14083a87618d6389b7bdda02f461',1,1800000000.0,1800000600.0,'{"duplicate":false,"revision":1,"seq":1,"work_id":"00000000000000000000000000000001"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','native-work:eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25','synthetic-key-2','work-start','go1','6dd044ec168fc3bbc6b2bba4a4e253ed64945550f04ced4605c9c67462aef5e4',2,1800000000.0,1800000600.0,'{"claim":{"generation":1,"revision":1,"expires_at":1800003600},"duplicate":false,"revision":2,"seq":2,"work_id":"00000000000000000000000000000001"}','eebf4ed00ef7eb9b9bac6558e7dd8ceccebc45528c9769ffa946230f759f7d25');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-3','work-create','go1','fe41f02e8cfbdc6bf95ffde825a483bc11ae5f291a8420cb151c94fab0aeb849',3,1800000000.0,1800000600.0,'{"duplicate":false,"revision":1,"seq":3,"work_id":"00000000000000000000000000000002"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-4','work-start','go1','0748db094c726afd56dd91ff96a290d7c146ec46f976168bb6b628911b13ed96',4,1800000000.0,1800000600.0,'{"claim":{"generation":2,"revision":1,"expires_at":1800003600},"duplicate":false,"revision":2,"seq":4,"work_id":"00000000000000000000000000000002"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-5','work-create','go1','1b89f8497a8a425f3fb1c1237ed64c066fcbc8f7b9df2b66dd3ad65973233c6b',5,1800000000.0,1800000600.0,'{"duplicate":false,"revision":1,"seq":5,"work_id":"00000000000000000000000000000003"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-6','work-edit','go1','1426889a4271a250f31d57db0e633fd398a9a8343c0775ac756800690e2dfae2',6,1800000000.0,1800000600.0,'{"duplicate":false,"revision":2,"seq":6,"work_id":"00000000000000000000000000000003"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-7','work-create','go1','086297ea015c1b5aaa6fe3b873785f621d49eef4ea52f197cd9009bd76a49a4d',7,1800000000.0,1800000600.0,'{"duplicate":false,"revision":1,"seq":7,"work_id":"00000000000000000000000000000004"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-8','work-start','go1','c8ea0edb39ba6f6a51ca569393b97730dd5ec1554dd49d6cfbd2d53ff02932bd',8,1800000000.0,1800000600.0,'{"claim":{"generation":3,"revision":1,"expires_at":1800000900},"duplicate":false,"revision":2,"seq":8,"work_id":"00000000000000000000000000000004"}','');
INSERT INTO work_replays VALUES('6e2f7f3cc99bd7122664e83b68cba79e','participant:claude-fixture-repo','synthetic-key-9','work-finish','go1','66c95054b8bad4c0623fab6e867716c7742a30f311500f133d102aa6328cb08c',9,1800000000.0,1800000600.0,'{"duplicate":false,"revision":3,"seq":9,"work_id":"00000000000000000000000000000004"}','');
CREATE TABLE audit (
				id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, action TEXT NOT NULL,
				target TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT ''
			);
CREATE TABLE imports (
			source TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('inbox','memory')),
			digest TEXT NOT NULL, cutoff TEXT NOT NULL, counts TEXT NOT NULL, at INTEGER NOT NULL
		);
CREATE TABLE participant_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, address TEXT NOT NULL,
				former TEXT NOT NULL, holder TEXT NOT NULL, reason TEXT NOT NULL,
				actor TEXT NOT NULL CHECK (actor IN ('session','maintainer','daemon')),
				details TEXT NOT NULL DEFAULT ''
			);
INSERT INTO participant_events VALUES(1,1800000000000,'codex-fixture-repo-worker','','codex-fixture-repo-dd','only_qualifier','session','');
INSERT INTO participant_events VALUES(2,1800000000000,'claude-fixture-repo','','claude-fixture-repo-74','only_qualifier','session','');
CREATE TABLE participant_fences (
				address TEXT NOT NULL, family TEXT NOT NULL, session_id TEXT NOT NULL,
				at INTEGER NOT NULL, reason TEXT NOT NULL, PRIMARY KEY (address, family, session_id)
			);
CREATE TABLE settings (
			name TEXT PRIMARY KEY, value INTEGER NOT NULL, updated_at INTEGER NOT NULL
		);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('participant_events',2);
CREATE UNIQUE INDEX names_peer ON names(family, session_id) WHERE kind='peer';
CREATE INDEX messages_sender ON messages(sender_family, sender_id, id);
CREATE INDEX memory_entries_live ON memory_entries(repository, superseded_by, revoked_by, expires);
CREATE INDEX memory_snapshots_owner ON memory_snapshots(repository, consumer);
CREATE INDEX work_events_item ON work_events(store, work_id, seq);
CREATE INDEX messages_wake ON messages(delivery_state, wake_next_at, recipient_family, recipient_id);
CREATE INDEX audit_at ON audit(at);
CREATE UNIQUE INDEX names_participant ON names(family, repository, role) WHERE kind='alias';
CREATE INDEX participant_events_at ON participant_events(at);
CREATE INDEX participant_events_address ON participant_events(address, id);
COMMIT;
PRAGMA user_version=16;
