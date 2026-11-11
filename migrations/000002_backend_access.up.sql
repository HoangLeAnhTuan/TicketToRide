BEGIN;

-- Provision this role with a separately managed login credential/secret.
-- Execute migrations as the deployment/migration role, never as this runtime role.
DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ttr_backend') THEN
        CREATE ROLE ttr_backend NOLOGIN NOSUPERUSER NOBYPASSRLS;
    END IF;
END $$;

REVOKE ALL ON public.rooms, public.room_players,
    public.game_snapshots, public.room_commands
    FROM PUBLIC, anon, authenticated, service_role;

ALTER TABLE public.rooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.room_players ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.game_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.room_commands ENABLE ROW LEVEL SECURITY;

GRANT USAGE ON SCHEMA public TO ttr_backend;
GRANT USAGE ON SCHEMA ttr_private TO ttr_backend;
GRANT SELECT ON ttr_private.schema_migrations TO ttr_backend;
GRANT SELECT, INSERT, UPDATE, DELETE ON public.rooms,
    public.room_players, public.game_snapshots, public.room_commands
    TO ttr_backend;

CREATE POLICY rooms_backend_only ON public.rooms
    FOR ALL TO ttr_backend USING (true) WITH CHECK (true);
CREATE POLICY players_backend_only ON public.room_players
    FOR ALL TO ttr_backend USING (true) WITH CHECK (true);
CREATE POLICY snapshots_backend_only ON public.game_snapshots
    FOR ALL TO ttr_backend USING (true) WITH CHECK (true);
CREATE POLICY commands_backend_only ON public.room_commands
    FOR ALL TO ttr_backend USING (true) WITH CHECK (true);

COMMIT;
