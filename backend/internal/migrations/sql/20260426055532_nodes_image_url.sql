-- +goose Up
-- Adds optional image_url to nodes. Renders as a circular photo on the node
-- in the graph view when set; otherwise the node is a colored circle by type.
-- Phase 5 (S3 upload pipeline) will populate this; for now only seed data and
-- external URLs touch it.
ALTER TABLE nodes ADD COLUMN image_url TEXT;

-- +goose Down
ALTER TABLE nodes DROP COLUMN image_url;
