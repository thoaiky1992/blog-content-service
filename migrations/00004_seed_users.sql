-- +goose Up
-- Fixed IDs so the README, tests and load tests can use them as X-User-ID.
INSERT INTO users (name, role) VALUES
    ('Admin User',    'admin'),
    ('Editor User',   'editor'),
    ('Reader User',   'reader'),
    ('Reporter User', 'reporter')
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DELETE FROM users;
