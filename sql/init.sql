-- App flow
-- - User A joins a room
-- - User B joins a room
-- - User A sends an offer to User B
-- - User B sends an answer to User A
-- - User A sends an ICE candidate to User B

-- Logical flow
-- User A sends an offer(SDP) -> stored in signaling table
-- User B receives an offer(SDP) via WAL subscription
-- Both users exchange ICE candidates -> stored in ice_candidates table
-- WebRTC establishes a connection -> stored in participants table
-- WebRTC uses this information to establish a direct peer to peer connection



-- Rooms
-- Rooms are the logical groupings of participants
CREATE TABLE IF NOT EXISTS rooms (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name       TEXT,
    created_at TIMESTAMPTZ DEFAULT now(),
    closed_at  TIMESTAMPTZ
);

-- Participants
-- Participants are the users in a room
CREATE TABLE IF NOT EXISTS participants (
    id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    room_id   UUID REFERENCES rooms(id),
    user_id   TEXT NOT NULL,
    joined_at TIMESTAMPTZ DEFAULT now(),
    left_at   TIMESTAMPTZ,
    UNIQUE (room_id, user_id)
);

-- SDP signaling
-- SDP offer/answer
-- SDP means Session Description Protocol
-- https://en.wikipedia.org/wiki/Session_Description_Protocol
-- It describes the media capabilities of your browser(video/audio, resolution, codecs, etc)
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
-- https://en.wikipedia.org/wiki/Interactive_Connectivity_Establishment
-- ICE stands for Interactive Connectivity Establishment
-- It is a protocol for establishing a connection between two peers
-- Network addresses(IP:port combinations) where your browser can connect to
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


