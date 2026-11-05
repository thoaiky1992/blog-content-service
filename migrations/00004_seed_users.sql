-- +goose Up
-- The database generates UUID v7 IDs; see README for the X-User-ID lookup command.
INSERT INTO users (name, role) VALUES
    ('Admin User',    'admin'),
    ('Editor User',   'editor'),
    ('Reader User',   'reader'),
    ('Reporter User', 'reporter')
ON CONFLICT (id) DO NOTHING;

-- +goose Down
DELETE FROM users;
