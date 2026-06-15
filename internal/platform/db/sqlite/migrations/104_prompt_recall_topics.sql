CREATE TABLE IF NOT EXISTS prompt_recall_topics (
    cwd TEXT NOT NULL,
    topic TEXT NOT NULL,
    template_id INTEGER NOT NULL,
    section_key TEXT NOT NULL,
    PRIMARY KEY(cwd, topic),
    CHECK(trim(cwd) <> ''),
    CHECK(trim(topic) <> ''),
    CHECK(template_id >= 0)
);
