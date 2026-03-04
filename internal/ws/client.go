package ws

import (
	"log"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yeboahd24/vidoe-call/internal/signaling"
)

const (
	writeWait      = 10 * time.Second    // max time to write a message to the peer
	pongWait       = 60 * time.Second    // max time to wait for a pong from the peer
	pingPeriod     = (pongWait * 9) / 10 // send pings at this interval
	maxMessageSize = 8 * 1024            // max incoming message size (8KB — SDPs can be large)
)

// Client is the glue between a single browser WebSocket connection
// and the signaling hub. It has two goroutines:
//   - readPump:  browser → hub (incoming signals)
//   - writePump: hub → browser (outgoing signals)
type Client struct {
	UserID string
	RoomID string
	Conn   *websocket.Conn
	Send   chan signaling.Signal

	// Hook called when a message arrives from the browser.
	// Set by the HTTP handler in main.go.
	OnMessage func(msg signaling.Signal)

	// Hook called when the connection closes.
	OnClose func()
}

func NewClient(userID, roomID string, conn *websocket.Conn) *Client {
	return &Client{
		UserID: userID,
		RoomID: roomID,
		Conn:   conn,
		Send:   make(chan signaling.Signal, 32),
	}
}

// Run starts both pumps. Blocks until the connection closes.
func (c *Client) Run() {
	go c.writePump()
	c.readPump() // blocks
}

// readPump reads messages from the browser and forwards them
// to the OnMessage callback (which writes them into PostgreSQL).
func (c *Client) readPump() {
	defer func() {
		c.Conn.Close()
		if c.OnClose != nil {
			c.OnClose()
		}
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		var msg signaling.Signal
		if err := c.Conn.ReadJSON(&msg); err != nil {
			if websocket.IsUnexpectedCloseError(err,
				websocket.CloseGoingAway,
				websocket.CloseAbnormalClosure,
			) {
				log.Printf("ws read error [user=%s]: %v", c.UserID, err)
			}
			break
		}

		// Stamp identity fields so the browser can't spoof them
		msg.FromUser = c.UserID
		msg.RoomID = c.RoomID

		if c.OnMessage != nil {
			c.OnMessage(msg)
		}
	}
}

// writePump drains the Send channel and writes messages to the browser.
// Also sends WebSocket pings to keep the connection alive.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel — send a close frame
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.Conn.WriteJSON(msg); err != nil {
				log.Printf("ws write error [user=%s]: %v", c.UserID, err)
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
