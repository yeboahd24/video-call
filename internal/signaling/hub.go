package signaling

import (
	"encoding/json"
	"log"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/yeboahd24/vidoe-call/internal/wal"
)

// Message sent to browsers over WebSocket
type Signal struct {
	Type      string `json:"type"` // offer | answer | ice | join | leave
	FromUser  string `json:"from_user"`
	ToUser    string `json:"to_user,omitempty"`
	RoomID    string `json:"room_id"`
	SDP       string `json:"sdp,omitempty"`
	Candidate any    `json:"candidate,omitempty"`
}

type Client struct {
	UserID string
	RoomID string
	Conn   *websocket.Conn
	Send   chan Signal
}

type Hub struct {
	mu    sync.RWMutex
	rooms map[string]map[string]*Client // roomID → userID → client
	WAL   <-chan wal.ChangeEvent
}

func NewHub(walEvents <-chan wal.ChangeEvent) *Hub {
	h := &Hub{
		rooms: make(map[string]map[string]*Client),
		WAL:   walEvents,
	}
	go h.processWAL()
	return h
}

// Route WAL events → WebSocket clients
func (h *Hub) processWAL() {
	for event := range h.WAL {
		switch event.Table {
		case "signaling":
			h.routeSignal(event.New)
		case "ice_candidates":
			h.routeICE(event.New)
		case "participants":
			h.routeParticipant(event)
		}
	}
}

func (h *Hub) routeSignal(row map[string]any) {
	sig := Signal{
		Type:     row["type"].(string),
		FromUser: row["from_user"].(string),
		RoomID:   row["room_id"].(string),
		SDP:      row["sdp"].(string),
	}
	toUser, _ := row["to_user"].(string)
	sig.ToUser = toUser

	h.mu.RLock()
	defer h.mu.RUnlock()

	room := h.rooms[sig.RoomID]
	if toUser != "" {
		// Directed: send only to target
		if c, ok := room[toUser]; ok {
			c.Send <- sig
		}
	} else {
		// Broadcast offer to everyone in room except sender
		for uid, c := range room {
			if uid != sig.FromUser {
				c.Send <- sig
			}
		}
	}
}

func (h *Hub) routeICE(row map[string]any) {
	var candidate any
	json.Unmarshal([]byte(row["candidate"].(string)), &candidate)

	sig := Signal{
		Type:      "ice",
		FromUser:  row["from_user"].(string),
		RoomID:    row["room_id"].(string),
		Candidate: candidate,
	}
	toUser, _ := row["to_user"].(string)

	h.mu.RLock()
	defer h.mu.RUnlock()
	room := h.rooms[sig.RoomID]

	if toUser != "" {
		if c, ok := room[toUser]; ok {
			c.Send <- sig
		}
	} else {
		for uid, c := range room {
			if uid != sig.FromUser {
				c.Send <- sig
			}
		}
	}
}

func (h *Hub) routeParticipant(event wal.ChangeEvent) {
	// Notify room members of join/leave
	var sig Signal
	if event.Op == "INSERT" {
		sig = Signal{
			Type: "join", FromUser: event.New["user_id"].(string),
			RoomID: event.New["room_id"].(string),
		}
	} else {
		sig = Signal{
			Type: "leave", FromUser: event.New["user_id"].(string),
			RoomID: event.New["room_id"].(string),
		}
	}
	h.broadcast(sig.RoomID, sig.FromUser, sig)
}

func (h *Hub) Register(c *Client) {
	h.mu.Lock()
	if h.rooms[c.RoomID] == nil {
		h.rooms[c.RoomID] = map[string]*Client{}
	}
	h.rooms[c.RoomID][c.UserID] = c
	h.mu.Unlock()
	go c.writePump()
}

func (h *Hub) Unregister(c *Client) {
	h.mu.Lock()
	delete(h.rooms[c.RoomID], c.UserID)
	h.mu.Unlock()
}

func (h *Hub) broadcast(roomID, exceptUser string, sig Signal) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for uid, c := range h.rooms[roomID] {
		if uid != exceptUser {
			c.Send <- sig
		}
	}
}

func (c *Client) writePump() {
	for sig := range c.Send {
		if err := c.Conn.WriteJSON(sig); err != nil {
			log.Printf("ws write error: %v", err)
			return
		}
	}
}
