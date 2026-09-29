ALTER TABLE users ADD COLUMN bio text NOT NULL DEFAULT '' CHECK (char_length(bio) <= 500);
ALTER TABLE users ADD COLUMN website text NOT NULL DEFAULT '' CHECK (char_length(website) <= 300);
ALTER TABLE users ADD COLUMN location text NOT NULL DEFAULT '' CHECK (char_length(location) <= 100);

CREATE TABLE profile_repositories (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    repository_id uuid NOT NULL REFERENCES repositories(id) ON DELETE CASCADE,
    position integer NOT NULL CHECK (position BETWEEN 1 AND 6),
    PRIMARY KEY (user_id, repository_id),
    UNIQUE (user_id, position)
);
