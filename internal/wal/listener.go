package wal

import (
	"context"
	"log"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
)

type ChangeEvent struct {
	Table string
	Op    string // INSERT | UPDATE | DELETE
	New   map[string]any
	Old   map[string]any
}

type Listener struct {
	conn   *pgconn.PgConn
	Events chan ChangeEvent
}

func New(connStr string) (*Listener, error) {
	// Must use replication=database connection param
	conn, err := pgconn.Connect(context.Background(),
		connStr+"?replication=database")
	if err != nil {
		return nil, err
	}
	return &Listener{conn: conn, Events: make(chan ChangeEvent, 256)}, nil
}

func (l *Listener) Start(ctx context.Context, slotName string) error {
	// Create replication slot if not exists
	_, err := pglogrepl.CreateReplicationSlot(ctx, l.conn, slotName,
		"pgoutput", pglogrepl.CreateReplicationSlotOptions{Temporary: true})
	if err != nil {
		log.Printf("slot may already exist: %v", err)
	}

	err = pglogrepl.StartReplication(ctx, l.conn, slotName, 0,
		pglogrepl.StartReplicationOptions{
			PluginArgs: []string{
				"proto_version '1'",
				"publication_names 'video_signaling'",
			},
		})
	if err != nil {
		return err
	}

	go l.consume(ctx)
	return nil
}

func (l *Listener) consume(ctx context.Context) {
	relations := map[uint32]*pglogrepl.RelationMessage{}

	for {
		msg, err := l.conn.ReceiveMessage(ctx)
		if err != nil {
			log.Printf("WAL receive error: %v", err)
			return
		}

		cd, ok := msg.(*pgproto3.CopyData)
		if !ok {
			continue
		}

		walMsg, err := pglogrepl.Parse(cd.Data)
		if err != nil {
			continue
		}

		switch m := walMsg.(type) {
		case *pglogrepl.RelationMessage:
			relations[m.RelationID] = m

		case *pglogrepl.InsertMessage:
			rel := relations[m.RelationID]
			row := decodeRow(rel, m.Tuple)
			l.Events <- ChangeEvent{Table: rel.RelationName, Op: "INSERT", New: row}

		case *pglogrepl.UpdateMessage:
			rel := relations[m.RelationID]
			l.Events <- ChangeEvent{
				Table: rel.RelationName, Op: "UPDATE",
				New: decodeRow(rel, m.NewTuple),
				Old: decodeRow(rel, m.OldTuple),
			}
		}
	}
}

func decodeRow(rel *pglogrepl.RelationMessage, tuple *pglogrepl.TupleData) map[string]any {
	row := map[string]any{}
	if tuple == nil {
		return row
	}
	for i, col := range tuple.Columns {
		if i < len(rel.Columns) {
			row[rel.Columns[i].Name] = string(col.Data)
		}
	}
	return row
}
