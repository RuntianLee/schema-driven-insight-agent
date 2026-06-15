CREATE TABLE IF NOT EXISTS memory_items (
	rowid INTEGER PRIMARY KEY,
	item_id TEXT NOT NULL UNIQUE,
	source_type TEXT NOT NULL CHECK (source_type IN ('trajectory', 'eval', 'manual')),
	source_id TEXT,
	adapter TEXT NOT NULL,
	task_id TEXT NOT NULL,
	task_class TEXT NOT NULL,
	question TEXT NOT NULL,
	summary TEXT NOT NULL,
	answer_outline TEXT NOT NULL,
	tools_json TEXT NOT NULL DEFAULT '[]',
	tags_json TEXT NOT NULL DEFAULT '[]',
	score REAL NOT NULL DEFAULT 0,
	used_count INTEGER NOT NULL DEFAULT 0,
	last_used_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	schema_version TEXT NOT NULL DEFAULT '1'
);

CREATE UNIQUE INDEX IF NOT EXISTS memory_items_source_unique
	ON memory_items(source_type, source_id)
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
	WITH RECURSIVE
		q(i, token) AS (
			SELECT 1, substr(new.question, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.question, i + 1, 2) FROM q WHERE i < length(new.question) - 1
		),
		s(i, token) AS (
			SELECT 1, substr(new.summary, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.summary, i + 1, 2) FROM s WHERE i < length(new.summary) - 1
		),
		a(i, token) AS (
			SELECT 1, substr(new.answer_outline, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.answer_outline, i + 1, 2) FROM a WHERE i < length(new.answer_outline) - 1
		),
		tl(i, token) AS (
			SELECT 1, substr(new.tools_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.tools_json, i + 1, 2) FROM tl WHERE i < length(new.tools_json) - 1
		),
		tg(i, token) AS (
			SELECT 1, substr(new.tags_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.tags_json, i + 1, 2) FROM tg WHERE i < length(new.tags_json) - 1
		)
	SELECT
		new.rowid,
		new.question || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM q WHERE length(token) = 2), ''),
		new.summary || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM s WHERE length(token) = 2), ''),
		new.answer_outline || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM a WHERE length(token) = 2), ''),
		new.tools_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tl WHERE length(token) = 2), ''),
		new.tags_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tg WHERE length(token) = 2), '');
END;

CREATE TRIGGER IF NOT EXISTS memory_items_ad AFTER DELETE ON memory_items BEGIN
	INSERT INTO memory_items_fts(memory_items_fts, rowid, question, summary, answer_outline, tools, tags)
	WITH RECURSIVE
		q(i, token) AS (
			SELECT 1, substr(old.question, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.question, i + 1, 2) FROM q WHERE i < length(old.question) - 1
		),
		s(i, token) AS (
			SELECT 1, substr(old.summary, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.summary, i + 1, 2) FROM s WHERE i < length(old.summary) - 1
		),
		a(i, token) AS (
			SELECT 1, substr(old.answer_outline, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.answer_outline, i + 1, 2) FROM a WHERE i < length(old.answer_outline) - 1
		),
		tl(i, token) AS (
			SELECT 1, substr(old.tools_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.tools_json, i + 1, 2) FROM tl WHERE i < length(old.tools_json) - 1
		),
		tg(i, token) AS (
			SELECT 1, substr(old.tags_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.tags_json, i + 1, 2) FROM tg WHERE i < length(old.tags_json) - 1
		)
	SELECT
		'delete',
		old.rowid,
		old.question || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM q WHERE length(token) = 2), ''),
		old.summary || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM s WHERE length(token) = 2), ''),
		old.answer_outline || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM a WHERE length(token) = 2), ''),
		old.tools_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tl WHERE length(token) = 2), ''),
		old.tags_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tg WHERE length(token) = 2), '');
END;

CREATE TRIGGER IF NOT EXISTS memory_items_au AFTER UPDATE ON memory_items BEGIN
	INSERT INTO memory_items_fts(memory_items_fts, rowid, question, summary, answer_outline, tools, tags)
	WITH RECURSIVE
		q(i, token) AS (
			SELECT 1, substr(old.question, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.question, i + 1, 2) FROM q WHERE i < length(old.question) - 1
		),
		s(i, token) AS (
			SELECT 1, substr(old.summary, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.summary, i + 1, 2) FROM s WHERE i < length(old.summary) - 1
		),
		a(i, token) AS (
			SELECT 1, substr(old.answer_outline, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.answer_outline, i + 1, 2) FROM a WHERE i < length(old.answer_outline) - 1
		),
		tl(i, token) AS (
			SELECT 1, substr(old.tools_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.tools_json, i + 1, 2) FROM tl WHERE i < length(old.tools_json) - 1
		),
		tg(i, token) AS (
			SELECT 1, substr(old.tags_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(old.tags_json, i + 1, 2) FROM tg WHERE i < length(old.tags_json) - 1
		)
	SELECT
		'delete',
		old.rowid,
		old.question || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM q WHERE length(token) = 2), ''),
		old.summary || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM s WHERE length(token) = 2), ''),
		old.answer_outline || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM a WHERE length(token) = 2), ''),
		old.tools_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tl WHERE length(token) = 2), ''),
		old.tags_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tg WHERE length(token) = 2), '');
	INSERT INTO memory_items_fts(rowid, question, summary, answer_outline, tools, tags)
	WITH RECURSIVE
		q(i, token) AS (
			SELECT 1, substr(new.question, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.question, i + 1, 2) FROM q WHERE i < length(new.question) - 1
		),
		s(i, token) AS (
			SELECT 1, substr(new.summary, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.summary, i + 1, 2) FROM s WHERE i < length(new.summary) - 1
		),
		a(i, token) AS (
			SELECT 1, substr(new.answer_outline, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.answer_outline, i + 1, 2) FROM a WHERE i < length(new.answer_outline) - 1
		),
		tl(i, token) AS (
			SELECT 1, substr(new.tools_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.tools_json, i + 1, 2) FROM tl WHERE i < length(new.tools_json) - 1
		),
		tg(i, token) AS (
			SELECT 1, substr(new.tags_json, 1, 2)
			UNION ALL
			SELECT i + 1, substr(new.tags_json, i + 1, 2) FROM tg WHERE i < length(new.tags_json) - 1
		)
	SELECT
		new.rowid,
		new.question || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM q WHERE length(token) = 2), ''),
		new.summary || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM s WHERE length(token) = 2), ''),
		new.answer_outline || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM a WHERE length(token) = 2), ''),
		new.tools_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tl WHERE length(token) = 2), ''),
		new.tags_json || ' ' || COALESCE((SELECT group_concat(token, ' ') FROM tg WHERE length(token) = 2), '');
END;

CREATE TABLE IF NOT EXISTS memory_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
