package db

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yeboahd24/vidoe-call/internal/signaling"
)

type Queries struct {
	pool *pgxpool.Pool
}

func New(connStr string) (*Queries, error) {
	pool, err := pgxpool.New(context.Background(), connStr)
	if err != nil {
		return nil, err
	}
	return &Queries{pool: pool}, nil
}

func (q *Queries) Close() {
	q.pool.Close()
}

// CreateRoom inserts a new room and returns its UUID.
func (q *Queries) CreateRoom(ctx context.Context) (string, error) {
	var id string
	err := q.pool.QueryRow(ctx,
		`INSERT INTO rooms (name) VALUES (NULL) RETURNING id`,
	).Scan(&id)
	return id, err
}

// JoinRoom records a participant joining a room.
func (q *Queries) JoinRoom(ctx context.Context, roomID, userID string) error {
	_, err := q.pool.Exec(ctx,
		`INSERT INTO participants (room_id, user_id)
		 VALUES ($1, $2)
		 ON CONFLICT DO NOTHING`,
		roomID, userID,
	)
	return err
}

// LeaveRoom stamps the left_at time for a participant.
func (q *Queries) LeaveRoom(ctx context.Context, roomID, userID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE participants
		 SET left_at = now()
		 WHERE room_id = $1 AND user_id = $2 AND left_at IS NULL`,
		roomID, userID,
	)
	return err
}

// InsertSignal writes an SDP offer or answer into the signaling table.
// The WAL listener will pick this up and fan it out to the target peer.
func (q *Queries) InsertSignal(ctx context.Context, msg signaling.Signal) error {
	var toUser *string
	if msg.ToUser != "" {
		toUser = &msg.ToUser
	}
	_, err := q.pool.Exec(ctx,
		`INSERT INTO signaling (room_id, from_user, to_user, type, sdp)
		 VALUES ($1, $2, $3, $4, $5)`,
		msg.RoomID, msg.FromUser, toUser, msg.Type, msg.SDP,
	)
	return err
}

// InsertICE writes an ICE candidate into the ice_candidates table.
// The WAL listener will pick this up and fan it out to the target peer.
func (q *Queries) InsertICE(ctx context.Context, msg signaling.Signal) error {
	var toUser *string
	if msg.ToUser != "" {
		toUser = &msg.ToUser
	}
	candidateJSON, err := json.Marshal(msg.Candidate)
	if err != nil {
		return err
	}
	_, err = q.pool.Exec(ctx,
		`INSERT INTO ice_candidates (room_id, from_user, to_user, candidate)
		 VALUES ($1, $2, $3, $4)`,
		msg.RoomID, msg.FromUser, toUser, candidateJSON,
	)
	return err
}

// ListParticipants returns active (not yet left) users in a room.
// Useful for sending the current room state to a newly joined user.
func (q *Queries) ListParticipants(ctx context.Context, roomID string) ([]string, error) {
	rows, err := q.pool.Query(ctx,
		`SELECT user_id FROM participants
		 WHERE room_id = $1 AND left_at IS NULL`,
		roomID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

// CloseRoom stamps closed_at on the room record.
func (q *Queries) CloseRoom(ctx context.Context, roomID string) error {
	_, err := q.pool.Exec(ctx,
		`UPDATE rooms SET closed_at = now() WHERE id = $1`,
		roomID,
	)
	return err
}
