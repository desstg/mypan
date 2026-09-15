ALTER TABLE quarktv_bindings ADD COLUMN preferred_resolution TEXT NOT NULL DEFAULT 'auto';
ALTER TABLE quarktv_bindings ADD COLUMN allow_dolby INTEGER NOT NULL DEFAULT 0;
