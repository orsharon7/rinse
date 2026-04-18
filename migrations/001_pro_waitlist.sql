-- Migration: create pro_waitlist table
-- Apply this in your Supabase project under SQL Editor or via the Supabase CLI.

CREATE TABLE IF NOT EXISTS pro_waitlist (
    id             BIGSERIAL PRIMARY KEY,
    email          TEXT        NOT NULL,
    github_username TEXT       NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    CONSTRAINT pro_waitlist_email_unique UNIQUE (email)
);

-- Index for chronological listing (used by the admin export)
CREATE INDEX IF NOT EXISTS pro_waitlist_created_at_idx ON pro_waitlist (created_at ASC);

-- Enable Row Level Security.  The server uses the service_role key which
-- bypasses RLS, so we lock down public access completely.
ALTER TABLE pro_waitlist ENABLE ROW LEVEL SECURITY;

-- Deny all access via the anon / authenticated roles.
-- The waitlist-server communicates exclusively with the service_role key.
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_policies
    WHERE tablename = 'pro_waitlist' AND policyname = 'deny_all'
  ) THEN
    EXECUTE $policy$
      CREATE POLICY deny_all ON pro_waitlist
        FOR ALL
        TO anon, authenticated
        USING (false)
    $policy$;
  END IF;
END
$$;
