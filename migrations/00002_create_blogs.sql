-- +goose Up
CREATE TABLE blogs (
    id          uuid        PRIMARY KEY     DEFAULT     uuidv7(),
    title       text        NOT NULL
                            CHECK (char_length(title) BETWEEN 1 AND 255),
    author      text        NOT NULL
                            CHECK (char_length(author) BETWEEN 1 AND 100),
    content     text        NOT NULL
                            CHECK (char_length(content) BETWEEN 1 AND 100000),
    description text        NOT NULL DEFAULT ''
                            CHECK (char_length(description) <= 500),
    views       bigint      NOT NULL DEFAULT 0
                            CHECK (views >= 0),
    version     integer     NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL,
    updated_at  timestamptz NOT NULL,
    deleted_at  timestamptz,
    created_by  uuid        NOT NULL REFERENCES users(id),
    updated_by  uuid        NOT NULL REFERENCES users(id),
    deleted_by  uuid        REFERENCES users(id),
    CONSTRAINT blogs_deleted_pair
        CHECK ((deleted_at IS NULL) = (deleted_by IS NULL)),
    CONSTRAINT blogs_updated_after_created
        CHECK (updated_at >= created_at),
    CONSTRAINT blogs_deleted_after_created
        CHECK (deleted_at IS NULL OR deleted_at >= created_at)
);

CREATE INDEX blogs_active_created_idx
ON blogs (created_at DESC, id DESC)
WHERE deleted_at IS NULL;

-- +goose Down
DROP TABLE blogs;
