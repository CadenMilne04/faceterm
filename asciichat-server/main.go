package main

import (
	"log"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"
)

const maxClients = 2

// ---------- client ----------

type Client struct {
	conn *websocket.Conn
	send chan []byte
}

// ---------- server ----------

type Server struct {
	clients map[*Client]bool
	mu      sync.Mutex
}

func NewServer() *Server {
	return &Server{
		clients: make(map[*Client]bool),
	}
}

// add client (limit 2)
func (s *Server) add(c *Client) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.clients) >= maxClients {
		return false
	}

	s.clients[c] = true
	log.Println("client connected, total:", len(s.clients))
	return true
}

// remove client
func (s *Server) remove(c *Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.clients, c)
	close(c.send)

	log.Println("client disconnected, total:", len(s.clients))
}

// relay message to everyone except sender
func (s *Server) broadcast(sender *Client, msg []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for c := range s.clients {
		if c != sender {
			select {
			case c.send <- msg:
			default:
				// drop if slow
			}
		}
	}
}

// ---------- websocket ----------

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	client := &Client{
		conn: conn,
		send: make(chan []byte, 16),
	}

	if !s.add(client) {
		conn.WriteMessage(websocket.TextMessage, []byte("room full (2 clients max)"))
		conn.Close()
		return
	}

	go s.writer(client)
	s.reader(client)
}

// read loop
func (s *Server) reader(c *Client) {
	defer func() {
		s.remove(c)
		c.conn.Close()
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		// just relay raw bytes
		s.broadcast(c, msg)
	}
}

// write loop
func (s *Server) writer(c *Client) {
	for msg := range c.send {
		err := c.conn.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			return
		}
	}
}

// ---------- main ----------

func main() {
	s := NewServer()

	http.HandleFunc("/ws", s.handleWS)

	log.Println("ASCII relay server on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
