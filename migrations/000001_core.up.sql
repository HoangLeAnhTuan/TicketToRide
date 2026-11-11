BEGIN;

CREATE TABLE public.rooms (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    room_code varchar(6) NOT NULL UNIQUE
        CHECK (room_code ~ '^[A-HJ-NP-Z2-9]{6}$'),
    name text NOT NULL CHECK (char_length(btrim(name)) BETWEEN 1 AND 80),
    password_hash text,
    password_version bigint NOT NULL DEFAULT 1 CHECK (password_version > 0),
    host_id uuid REFERENCES auth.users(id) ON DELETE RESTRICT,
    max_players smallint NOT NULL CHECK (max_players BETWEEN 2 AND 5),
    status text NOT NULL DEFAULT 'WAITING'
        CHECK (status IN ('WAITING', 'PLAYING', 'FINISHED')),
    state_version bigint NOT NULL DEFAULT 0 CHECK (state_version >= 0),
    owner_instance uuid,
    owner_epoch bigint NOT NULL DEFAULT 0 CHECK (owner_epoch >= 0),
    lease_until timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    closed_at timestamptz,
    CHECK (closed_at IS NOT NULL OR host_id IS NOT NULL),
    CHECK ((owner_instance IS NULL) = (lease_until IS NULL))
);

CREATE TABLE public.room_players (
    room_id uuid NOT NULL REFERENCES public.rooms(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE RESTRICT,
    display_name text NOT NULL
        CHECK (char_length(btrim(display_name)) BETWEEN 1 AND 32),
    color text CHECK (color IN ('BLUE', 'RED', 'GREEN', 'YELLOW', 'BLACK')),
    is_ready boolean NOT NULL DEFAULT false,
    is_connected boolean NOT NULL DEFAULT false,
    joined_at timestamptz NOT NULL DEFAULT now(),
    seat smallint NOT NULL CHECK (seat BETWEEN 0 AND 4),
    PRIMARY KEY (room_id, user_id),
    UNIQUE (room_id, color),
    UNIQUE (room_id, seat)
);

ALTER TABLE public.rooms ADD CONSTRAINT rooms_host_is_member
    FOREIGN KEY (id, host_id)
    REFERENCES public.room_players(room_id, user_id)
    DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE public.game_snapshots (
    room_id uuid PRIMARY KEY REFERENCES public.rooms(id) ON DELETE CASCADE,
    turn_number bigint NOT NULL DEFAULT 0 CHECK (turn_number >= 0),
    state_version bigint NOT NULL CHECK (state_version >= 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    ruleset_version text NOT NULL,
    catalog_hash text NOT NULL CHECK (catalog_hash ~ '^[0-9a-f]{64}$'),
    state_json jsonb NOT NULL CHECK (jsonb_typeof(state_json) = 'object'),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE public.room_commands (
    room_id uuid NOT NULL REFERENCES public.rooms(id) ON DELETE CASCADE,
    user_id uuid NOT NULL REFERENCES auth.users(id) ON DELETE RESTRICT,
    request_id uuid NOT NULL,
    command_hash text NOT NULL CHECK (command_hash ~ '^[0-9a-f]{64}$'),
    command_type text NOT NULL,
    state_version bigint NOT NULL CHECK (state_version > 0),
    response_json jsonb NOT NULL CHECK (jsonb_typeof(response_json) = 'object'),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (room_id, user_id, request_id)
);

CREATE INDEX rooms_waiting_created_idx
    ON public.rooms (created_at DESC, id) WHERE status = 'WAITING';
CREATE INDEX rooms_host_idx ON public.rooms (host_id);
CREATE INDEX room_players_user_idx ON public.room_players (user_id, room_id);
CREATE INDEX room_players_join_order_idx
    ON public.room_players (room_id, joined_at, user_id);
CREATE INDEX room_commands_retention_idx
    ON public.room_commands (room_id, created_at);

-- Fail closed even when deployment stops before the role-grant migration.
REVOKE ALL ON public.rooms, public.room_players, public.game_snapshots,
    public.room_commands FROM PUBLIC, anon, authenticated, service_role;
ALTER TABLE public.rooms ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.room_players ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.game_snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.room_commands ENABLE ROW LEVEL SECURITY;

COMMIT;
