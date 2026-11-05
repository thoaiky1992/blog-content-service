-- +goose Up
CREATE TABLE users (
    id          uuid        PRIMARY KEY     DEFAULT         uuidv7(),
    name        text        NOT NULL,
    role        text        NOT NULL
                            CHECK (role IN ('admin', 'editor', 'reader', 'reporter')),
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE users;
