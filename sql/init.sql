-- Rooms
CREATE TABLE IF NOT EXISTS rooms (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    closed_at  TIMESTAMPTZ
);

-- Participants
CREATE TABLE IF NOT EXISTS participants (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id   UUID REFERENCES rooms(id),
    user_id   TEXT NOT NULL,
    joined_at TIMESTAMPTZ DEFAULT now(),
    left_at   TIMESTAMPTZ,
    UNIQUE (room_id, user_id)
);

-- SDP signaling
CREATE TABLE IF NOT EXISTS signaling (
    id         BIGSERIAL PRIMARY KEY,
    room_id    UUID NOT NULL,
    from_user  TEXT NOT NULL,
    to_user    TEXT,
    type       TEXT NOT NULL CHECK (type IN ('offer','answer')),
    sdp        TEXT NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- ICE candidates
CREATE TABLE IF NOT EXISTS ice_candidates (
    id         BIGSERIAL PRIMARY KEY,
    room_id    UUID NOT NULL,
    from_user  TEXT NOT NULL,
    to_user    TEXT,
    candidate  JSONB NOT NULL,
    created_at TIMESTAMPTZ DEFAULT now()
);

-- Required for WAL logical replication
ALTER TABLE signaling      REPLICA IDENTITY FULL;
ALTER TABLE ice_candidates REPLICA IDENTITY FULL;
ALTER TABLE participants   REPLICA IDENTITY FULL;

-- Publication the Go WAL listener subscribes to
CREATE PUBLICATION video_signaling
    FOR TABLE signaling, ice_candidates, participants;
