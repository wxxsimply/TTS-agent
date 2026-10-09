-- Create readonly role for agent SQL execution
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'agent_readonly') THEN
        CREATE ROLE agent_readonly WITH LOGIN PASSWORD 'readonly_secret';
    END IF;
END
$$;

-- Enable pgvector extension
CREATE EXTENSION IF NOT EXISTS vector;

-- Grant readonly access (applied after tables created by seed)
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT ON TABLES TO agent_readonly;
