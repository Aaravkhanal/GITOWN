CREATE TABLE repository_topics (
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    topic text NOT NULL CHECK (topic ~ '^[a-z0-9][a-z0-9-]{0,31}$'),
    PRIMARY KEY (repository_id, topic)
);

CREATE INDEX repository_topics_topic ON repository_topics(topic, repository_id);
