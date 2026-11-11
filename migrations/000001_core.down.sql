BEGIN;
ALTER TABLE public.rooms DROP CONSTRAINT rooms_host_is_member;
DROP TABLE public.room_commands;
DROP TABLE public.game_snapshots;
DROP TABLE public.room_players;
DROP TABLE public.rooms;
COMMIT;
