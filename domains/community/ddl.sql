-- Community domain stub (Phase 5 placeholder)
CREATE TABLE IF NOT EXISTS community_members (
    id SERIAL PRIMARY KEY,
    name VARCHAR(128) NOT NULL,
    join_date DATE NOT NULL
);
