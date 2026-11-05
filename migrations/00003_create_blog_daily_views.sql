-- +goose Up
CREATE TABLE blog_daily_views (
    blog_id     uuid        NOT NULL REFERENCES blogs(id),
    view_date   date        NOT NULL,
    ip_address  inet        NOT NULL,
    created_at  timestamptz NOT NULL,

    PRIMARY KEY (blog_id, view_date, ip_address)
);

-- +goose Down
DROP TABLE blog_daily_views;
