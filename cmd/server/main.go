package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/websocket"
	"github.com/yeboahd24/vidoe-call/internal/db"
	"github.com/yeboahd24/vidoe-call/internal/signaling"
	"github.com/yeboahd24/vidoe-call/internal/wal"
	ws "github.com/yeboahd24/vidoe-call/internal/ws"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://video:secret@localhost:5432/videodb"
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Start WAL listener (needs replication=database connection)
	walListener, err := wal.New(connStr)
	if err != nil {
		log.Fatalf("wal init: %v", err)
	}
	if err := walListener.Start(context.Background(), "video_slot"); err != nil {
		log.Fatalf("wal start: %v", err)
	}

	hub := signaling.NewHub(walListener.Events)

	queries, err := db.New(connStr)
	if err != nil {
		log.Fatalf("db init: %v", err)
	}
	defer queries.Close()

	// Serve static frontend files
	http.Handle("/", http.FileServer(http.Dir("./frontend")))

	// REST: create a room
	http.HandleFunc("/rooms", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		roomID, err := queries.CreateRoom(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"room_id":"` + roomID + `"}`))
	})

	// REST: list active participants in a room
	http.HandleFunc("/rooms/", func(w http.ResponseWriter, r *http.Request) {
		roomID := r.URL.Path[len("/rooms/"):]
		users, err := queries.ListParticipants(r.Context(), roomID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		out := `{"participants":[`
		for i, u := range users {
			if i > 0 {
				out += ","
			}
			out += `"` + u + `"`
		}
		out += `]}`
		w.Write([]byte(out))
	})

	// WebSocket: /ws?room=<roomID>&user=<userID>
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		roomID := r.URL.Query().Get("room")
		userID := r.URL.Query().Get("user")
		if roomID == "" || userID == "" {
			http.Error(w, "room and user are required", http.StatusBadRequest)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}

		client := ws.NewClient(userID, roomID, conn)

		hubClient := &signaling.Client{
			UserID: userID,
			RoomID: roomID,
			Conn:   conn,
			Send:   client.Send,
		}
		hub.Register(hubClient)
		queries.JoinRoom(r.Context(), roomID, userID)

		client.OnMessage = func(msg signaling.Signal) {
			switch msg.Type {
			case "offer", "answer":
				if err := queries.InsertSignal(r.Context(), msg); err != nil {
					log.Printf("insert signal: %v", err)
				}
			case "ice":
				if err := queries.InsertICE(r.Context(), msg); err != nil {
					log.Printf("insert ice: %v", err)
				}
			}
		}

		client.OnClose = func() {
			hub.Unregister(hubClient)
			queries.LeaveRoom(context.Background(), roomID, userID)
		}

		client.Run()
	})

	log.Printf("Server listening on :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
