PRAGMA foreign_keys=OFF;
BEGIN TRANSACTION;
CREATE TABLE sessions (
			family TEXT NOT NULL, id TEXT NOT NULL, repository TEXT NOT NULL,
			directory TEXT NOT NULL, wake_target TEXT NOT NULL,
			registered_at INTEGER NOT NULL, renewed_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL, retired_at INTEGER NOT NULL DEFAULT 0,
			revision INTEGER NOT NULL, last_seq INTEGER NOT NULL DEFAULT 0, acked_through INTEGER NOT NULL DEFAULT 0, ack_mark INTEGER NOT NULL DEFAULT 0, ack_mark_at INTEGER NOT NULL DEFAULT 0, purge_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (family,id)
		);
INSERT INTO sessions VALUES('maintainer','maintainer','','','{}',0,0,9007199254740991,0,1,0,0,0,0,0);
INSERT INTO sessions VALUES('codex','synthetic-holder','/synthetic/koinon/.git','/synthetic/koinon','{"claude_pid":4242}',1790000000000,1790000000000,1790000900000,0,1,2,1,0,0,0);
INSERT INTO sessions VALUES('codex','synthetic-second','/synthetic/koinon/.git','/synthetic/koinon','{"claude_pid":4242}',1790000000000,1790000000000,1790000900000,0,1,0,0,0,0,0);
INSERT INTO sessions VALUES('claude','synthetic-claude','/synthetic/koinon/.git','/synthetic/koinon','{"claude_pid":4242}',1790000000000,1790000000000,1790000900000,0,1,1,0,0,0,0);
CREATE TABLE names (
				name TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('peer','alias')),
				family TEXT NOT NULL, session_id TEXT NOT NULL DEFAULT '',
				repository TEXT NOT NULL DEFAULT '', holder_id TEXT NOT NULL DEFAULT ''
			);
INSERT INTO names VALUES('maintainer','peer','maintainer','maintainer','','');
INSERT INTO names VALUES('codex-koinon-1c','peer','codex','synthetic-holder','/synthetic/koinon/.git','');
INSERT INTO names VALUES('codex-koinon','alias','codex','','/synthetic/koinon/.git','synthetic-holder');
INSERT INTO names VALUES('codex-koinon-e0','peer','codex','synthetic-second','/synthetic/koinon/.git','');
INSERT INTO names VALUES('claude-koinon-07','peer','claude','synthetic-claude','/synthetic/koinon/.git','');
INSERT INTO names VALUES('claude-koinon','alias','claude','','/synthetic/koinon/.git','synthetic-claude');
CREATE TABLE messages (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				recipient_family TEXT NOT NULL, recipient_id TEXT NOT NULL, seq INTEGER NOT NULL,
				sender_family TEXT NOT NULL, sender_id TEXT NOT NULL, sender_name TEXT NOT NULL,
				body TEXT NOT NULL, created_at INTEGER NOT NULL,
				delivery_state TEXT NOT NULL DEFAULT 'waiting'
					CHECK (delivery_state IN ('waiting','notified','uncertain','failed')),
				delivery_reason TEXT NOT NULL DEFAULT '', delivery_updated_at INTEGER NOT NULL, wake_attempts INTEGER NOT NULL DEFAULT 0, wake_next_at INTEGER NOT NULL DEFAULT 0, wake_reason TEXT NOT NULL DEFAULT '', maintainer_ack INTEGER NOT NULL DEFAULT 0,
				UNIQUE (recipient_family, recipient_id, seq)
			);
INSERT INTO messages VALUES(1,'codex','synthetic-holder',1,'claude','synthetic-claude','claude-koinon-07','synthetic one',1790000000000,'waiting','',1790000000000,0,0,'',0);
INSERT INTO messages VALUES(2,'codex','synthetic-holder',2,'claude','synthetic-claude','claude-koinon-07','synthetic two',1790000000000,'waiting','',1790000000000,0,0,'',0);
INSERT INTO messages VALUES(3,'claude','synthetic-claude',1,'codex','synthetic-second','codex-koinon-e0','synthetic to claude',1790000000000,'waiting','',1790000000000,0,0,'',0);
CREATE TABLE launches (
			id TEXT PRIMARY KEY, family TEXT NOT NULL, directory TEXT NOT NULL,
			target TEXT NOT NULL, created_at INTEGER NOT NULL
		);
CREATE TABLE memory_stores (repository TEXT PRIMARY KEY, store_id TEXT NOT NULL,
				head INTEGER NOT NULL DEFAULT 0, floor INTEGER NOT NULL DEFAULT 0, work_counter INTEGER NOT NULL DEFAULT 0, claim_counter INTEGER NOT NULL DEFAULT 0);
INSERT INTO memory_stores VALUES('/synthetic/koinon/.git','027711ed8b5e47ca0443b8b6931c6a93',4,0,1,1);
CREATE TABLE memory_entries (repository TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				type TEXT NOT NULL, scope TEXT NOT NULL, scope_target TEXT, path TEXT, body TEXT NOT NULL,
				author TEXT, writer_family TEXT NOT NULL, writer_name TEXT NOT NULL, consumer TEXT NOT NULL,
				revision INTEGER NOT NULL, supersedes INTEGER, revokes INTEGER, superseded_by INTEGER,
				revoked_by INTEGER, conflicts_with INTEGER, expires REAL, PRIMARY KEY (repository, seq));
INSERT INTO memory_entries VALUES('/synthetic/koinon/.git',1,1790000000.0,'finding','repo',NULL,NULL,'synthetic first',NULL,'codex','codex-koinon-e0','codex:synthetic-second',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/koinon/.git',2,1790000000.0,'finding','repo',NULL,NULL,'synthetic after the cursor',NULL,'codex','codex-koinon-e0','codex:synthetic-second',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/koinon/.git',3,1790000000.0,'work-event','repo','00000000000000000000000000000001',NULL,'',NULL,'codex','codex-koinon-1c','codex:synthetic-holder',1,NULL,NULL,NULL,NULL,NULL,NULL);
INSERT INTO memory_entries VALUES('/synthetic/koinon/.git',4,1790000000.0,'work-event','repo','00000000000000000000000000000001',NULL,'',NULL,'codex','codex-koinon-1c','codex:synthetic-holder',2,NULL,NULL,NULL,NULL,NULL,NULL);
CREATE TABLE memory_idem (repository TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER NOT NULL, ts REAL NOT NULL,
				deadline REAL NOT NULL, PRIMARY KEY (repository, consumer, key));
CREATE TABLE memory_cursors (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				issued INTEGER NOT NULL, snapshot TEXT, bootstrapped INTEGER NOT NULL, resnapshot INTEGER NOT NULL,
				updated REAL NOT NULL, PRIMARY KEY (repository, consumer));
INSERT INTO memory_cursors VALUES('/synthetic/koinon/.git','codex:synthetic-holder',1,1,NULL,1,0,1790000000.0);
CREATE TABLE memory_retired (repository TEXT NOT NULL, consumer TEXT NOT NULL, seq INTEGER NOT NULL,
				at REAL NOT NULL, PRIMARY KEY (repository, consumer));
CREATE TABLE memory_snapshots (id TEXT PRIMARY KEY, repository TEXT NOT NULL, consumer TEXT NOT NULL,
				head INTEGER NOT NULL, created REAL NOT NULL, items INTEGER NOT NULL, issued INTEGER NOT NULL,
				acked INTEGER NOT NULL DEFAULT 0, acked_at REAL);
INSERT INTO memory_snapshots VALUES('57c3bb7c3b12b305cf492e63ad40e208','/synthetic/koinon/.git','codex:synthetic-holder',1,1790000000.0,1,1,1,1790000000.0);
CREATE TABLE memory_snapshot_items (id TEXT NOT NULL, position INTEGER NOT NULL, seq INTEGER NOT NULL,
				payload TEXT NOT NULL, bytes INTEGER NOT NULL, PRIMARY KEY (id, position));
INSERT INTO memory_snapshot_items VALUES('57c3bb7c3b12b305cf492e63ad40e208',0,1,'{"seq":1,"ts":1790000000,"type":"finding","scope":"repo","scope_target":null,"path":null,"body":"synthetic first","author":null,"writer_family":"codex","writer_name":"codex-koinon-e0","consumer":"codex:synthetic-second","revision":1,"supersedes":null,"revokes":null,"superseded_by":null,"revoked_by":null,"conflicts_with":null,"expires":null}',343);
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
				latest_seq INTEGER NOT NULL CHECK (latest_seq>0),
				CHECK ((lifecycle='finished' AND outcome IS NOT NULL AND finished_at IS NOT NULL AND expires_at IS NOT NULL)
					OR (lifecycle<>'finished' AND outcome IS NULL AND finished_at IS NULL AND expires_at IS NULL)),
				PRIMARY KEY (store, work_id));
INSERT INTO work_items VALUES('027711ed8b5e47ca0443b8b6931c6a93','00000000000000000000000000000001',2,'active','Synthetic held item','synthetic criteria','synthetic non-goals',NULL,1790000000.0,'codex:synthetic-holder',2,1,1,1790000000.0,1790003600.0,'','synthetic start','synthetic artifact','','codex:synthetic-holder',1,1790000900.0,0,NULL,'','[]',NULL,NULL,4);
CREATE TABLE work_scope_revisions (store TEXT NOT NULL, work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0), ts REAL NOT NULL, consumer TEXT NOT NULL, author TEXT,
				title TEXT NOT NULL, criteria TEXT NOT NULL, non_goals TEXT NOT NULL,
				PRIMARY KEY (store, work_id, revision));
INSERT INTO work_scope_revisions VALUES('027711ed8b5e47ca0443b8b6931c6a93','00000000000000000000000000000001',1,1790000000.0,'codex:synthetic-holder',NULL,'Synthetic held item','synthetic criteria','synthetic non-goals');
CREATE TABLE claim_bundles (store TEXT NOT NULL, generation INTEGER NOT NULL CHECK (generation>0),
				work_id TEXT NOT NULL, consumer TEXT NOT NULL, revision INTEGER NOT NULL CHECK (revision>0),
				issued_at REAL NOT NULL, renewed_at REAL NOT NULL, expires_at REAL NOT NULL,
				progress_epoch INTEGER NOT NULL CHECK (progress_epoch>0),
				overdue_recorded INTEGER NOT NULL DEFAULT 0 CHECK (overdue_recorded IN (0,1)),
				active INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0,1)),
				overdue_credit INTEGER NOT NULL DEFAULT 1 CHECK (overdue_credit IN (0,1)),
				end_credit INTEGER NOT NULL DEFAULT 1 CHECK (end_credit IN (0,1)),
				PRIMARY KEY (store, generation), UNIQUE (store, work_id));
INSERT INTO claim_bundles VALUES('027711ed8b5e47ca0443b8b6931c6a93',1,'00000000000000000000000000000001','codex:synthetic-holder',1,1790000000.0,1790000000.0,1790000900.0,1,0,1,1,1);
CREATE TABLE claim_resources (store TEXT NOT NULL, generation INTEGER NOT NULL,
				ordinal INTEGER NOT NULL CHECK (ordinal BETWEEN 0 AND 8),
				kind TEXT NOT NULL CHECK (kind IN ('writer','path','exact')), resource TEXT NOT NULL,
				PRIMARY KEY (store, generation, ordinal));
INSERT INTO claim_resources VALUES('027711ed8b5e47ca0443b8b6931c6a93',1,0,'writer','00000000000000000000000000000001');
CREATE TABLE work_events (store TEXT NOT NULL, seq INTEGER NOT NULL CHECK (seq>0), work_id TEXT NOT NULL,
				revision INTEGER NOT NULL CHECK (revision>0),
				kind TEXT NOT NULL CHECK (kind IN ('created','proposed','edited','started','updated','released',
					'finished','progress-overdue','lease-expired')),
				payload TEXT NOT NULL, PRIMARY KEY (store, seq));
INSERT INTO work_events VALUES('027711ed8b5e47ca0443b8b6931c6a93',3,'00000000000000000000000000000001',1,'created','{"work_id":"00000000000000000000000000000001","revision":1,"lifecycle":"open","title":"Synthetic held item","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"created_at":1790000000,"created_consumer":"codex:synthetic-holder","first_start_revision":null,"scope_revision":1,"progress_epoch":0,"last_progress_at":null,"progress_deadline":null,"progress":"","checkpoint":"","next_artifact":"","blocker":"","last_writer":null,"last_generation":null,"last_lease_expires":null,"lease_expired":false,"outcome":null,"reason":"","references":null,"finished_at":null,"expires_at":null,"latest_seq":3,"type":"work-item","seq":3,"observed_at":1790000000,"observed_lease_expires":null,"current_claim":null,"lease_valid":false,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}');
INSERT INTO work_events VALUES('027711ed8b5e47ca0443b8b6931c6a93',4,'00000000000000000000000000000001',2,'started','{"work_id":"00000000000000000000000000000001","revision":2,"lifecycle":"active","title":"Synthetic held item","criteria":"synthetic criteria","non_goals":"synthetic non-goals","proposed_assignee":null,"created_at":1790000000,"created_consumer":"codex:synthetic-holder","first_start_revision":2,"scope_revision":1,"progress_epoch":1,"last_progress_at":1790000000,"progress_deadline":1790003600,"progress":"","checkpoint":"synthetic start","next_artifact":"synthetic artifact","blocker":"","last_writer":"codex:synthetic-holder","last_generation":1,"last_lease_expires":1790000900,"lease_expired":false,"outcome":null,"reason":"","references":[],"finished_at":null,"expires_at":null,"latest_seq":4,"type":"work-item","seq":4,"observed_at":1790000000,"observed_lease_expires":1790000900,"current_claim":{"consumer":"codex:synthetic-holder","generation":1,"revision":1,"expires_at":1790000900,"resources":[["writer","00000000000000000000000000000001"]]},"lease_valid":true,"progress_overdue":false,"progress_unverified":false,"progress_unverified_reasons":[],"scope_revisions":[1],"criteria_changed_after_start":false}');
CREATE TABLE work_replays (store TEXT NOT NULL, consumer TEXT NOT NULL, key TEXT NOT NULL,
				operation TEXT NOT NULL, scheme TEXT NOT NULL, fingerprint TEXT NOT NULL, seq INTEGER, ts REAL NOT NULL,
				deadline REAL NOT NULL, result TEXT NOT NULL, PRIMARY KEY (store, consumer, key));
INSERT INTO work_replays VALUES('027711ed8b5e47ca0443b8b6931c6a93','codex:synthetic-holder','synthetic-key-1','work-create','go1','413f4711f284405478365a37a937c341ba7ecc7aad265d2e3a2b63d67b0791f3',3,1790000000.0,1790000600.0,'{"duplicate":false,"revision":1,"seq":3,"work_id":"00000000000000000000000000000001"}');
INSERT INTO work_replays VALUES('027711ed8b5e47ca0443b8b6931c6a93','codex:synthetic-holder','synthetic-key-2','work-start','go1','a8a2c0407769b855235fe3d3d5b8f6cd663314f9cf0cb804db12316b2f6b5354',4,1790000000.0,1790000600.0,'{"claim":{"generation":1,"revision":1,"expires_at":1790000900},"duplicate":false,"revision":2,"seq":4,"work_id":"00000000000000000000000000000001"}');
CREATE TABLE audit (
				id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, action TEXT NOT NULL,
				target TEXT NOT NULL, result TEXT NOT NULL, reason TEXT NOT NULL DEFAULT ''
			);
CREATE TABLE imports (
			source TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('inbox','memory')),
			digest TEXT NOT NULL, cutoff TEXT NOT NULL, counts TEXT NOT NULL, at INTEGER NOT NULL
		);
DELETE FROM sqlite_sequence;
INSERT INTO sqlite_sequence VALUES('messages',3);
CREATE UNIQUE INDEX names_peer ON names(family, session_id) WHERE kind='peer';
CREATE UNIQUE INDEX names_alias ON names(family, repository) WHERE kind='alias';
CREATE INDEX messages_sender ON messages(sender_family, sender_id, id);
CREATE INDEX memory_entries_live ON memory_entries(repository, superseded_by, revoked_by, expires);
CREATE INDEX memory_snapshots_owner ON memory_snapshots(repository, consumer);
CREATE INDEX work_events_item ON work_events(store, work_id, seq);
CREATE INDEX messages_wake ON messages(delivery_state, wake_next_at, recipient_family, recipient_id);
CREATE INDEX audit_at ON audit(at);
COMMIT;
PRAGMA user_version=9;
