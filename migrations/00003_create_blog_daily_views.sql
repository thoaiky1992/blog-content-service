-- +goose Up
CREATE TABLE blog_daily_views (
    blog_id     uuid        NOT NULL REFERENCES blogs(id),
    view_date   date        NOT NULL,
    ip_hash     bytea       NOT NULL CHECK (octet_length(ip_hash) = 16),
    created_at  timestamptz NOT NULL,

    PRIMARY KEY (blog_id, view_date, ip_hash)
);

-- +goose Down
DROP TABLE blog_daily_views;
