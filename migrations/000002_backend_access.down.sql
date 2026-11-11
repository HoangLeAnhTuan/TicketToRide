BEGIN;
DROP POLICY IF EXISTS commands_backend_only ON public.room_commands;
DROP POLICY IF EXISTS snapshots_backend_only ON public.game_snapshots;
DROP POLICY IF EXISTS players_backend_only ON public.room_players;
DROP POLICY IF EXISTS rooms_backend_only ON public.rooms;
REVOKE ALL ON public.rooms, public.room_players,
    public.game_snapshots, public.room_commands FROM ttr_backend;
COMMIT;

