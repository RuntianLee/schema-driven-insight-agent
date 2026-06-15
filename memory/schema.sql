CREATE TABLE IF NOT EXISTS memory_items (
	rowid INTEGER PRIMARY KEY,
	item_id TEXT NOT NULL UNIQUE,
	source_type TEXT NOT NULL CHECK (source_type IN ('trajectory', 'eval', 'manual')),
	source_id TEXT,
	adapter TEXT NOT NULL,
	task_id TEXT,
	task_class TEXT,
	question TEXT NOT NULL,
	summary TEXT NOT NULL,
	answer_outline TEXT,
	tools_json TEXT NOT NULL DEFAULT '[]',
	tags_json TEXT NOT NULL DEFAULT '[]',
	tools TEXT GENERATED ALWAYS AS (tools_json) VIRTUAL,
	tags TEXT GENERATED ALWAYS AS (tags_json) VIRTUAL,
	score REAL NOT NULL DEFAULT 0,
	used_count INTEGER NOT NULL DEFAULT 0,
	last_used_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	schema_version TEXT NOT NULL DEFAULT '1'
);

CREATE UNIQUE INDEX IF NOT EXISTS memory_items_source_unique
	ON memory_items(adapter, source_type, source_id)
	WHERE source_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS memory_items_adapter_task_idx
	ON memory_items(adapter, task_id, task_class);

CREATE INDEX IF NOT EXISTS memory_items_score_idx
	ON memory_items(score);

CREATE INDEX IF NOT EXISTS memory_items_used_idx
	ON memory_items(used_count, last_used_at);

CREATE VIRTUAL TABLE IF NOT EXISTS memory_items_fts USING fts5(
	question,
	summary,
	answer_outline,
	tools,
	tags,
	content='memory_items',
	content_rowid='rowid'
);

CREATE TRIGGER IF NOT EXISTS memory_items_ai AFTER INSERT ON memory_items BEGIN
	INSERT INTO memory_items_fts(rowid, question, summary, answer_outline, tools, tags)
	VALUES (new.rowid, new.question, new.summary, COALESCE(new.answer_outline, ''), new.tools, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS memory_items_ad AFTER DELETE ON memory_items BEGIN
	INSERT INTO memory_items_fts(memory_items_fts, rowid, question, summary, answer_outline, tools, tags)
	VALUES ('delete', old.rowid, old.question, old.summary, COALESCE(old.answer_outline, ''), old.tools, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS memory_items_au AFTER UPDATE OF question, summary, answer_outline, tools_json, tags_json ON memory_items BEGIN
	INSERT INTO memory_items_fts(memory_items_fts, rowid, question, summary, answer_outline, tools, tags)
	VALUES ('delete', old.rowid, old.question, old.summary, COALESCE(old.answer_outline, ''), old.tools, old.tags);
	INSERT INTO memory_items_fts(rowid, question, summary, answer_outline, tools, tags)
	VALUES (new.rowid, new.question, new.summary, COALESCE(new.answer_outline, ''), new.tools, new.tags);
END;

CREATE TABLE IF NOT EXISTS memory_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
