package api

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSClient WebSocket 单客户端封装
type WSClient struct {
	hub  *WSHub
	conn *websocket.Conn
	send chan []byte
}

// WSHub WebSocket 异步广播管理中心
type WSHub struct {
	clients   map[*WSClient]bool
	broadcast chan []byte
	mu        sync.RWMutex
}

// NewWSHub 初始化 WebSocket Hub
func NewWSHub() *WSHub {
	h := &WSHub{
		clients:   make(map[*WSClient]bool),
		broadcast: make(chan []byte, 1024),
	}
	go h.run()
	return h
}

func (h *WSHub) run() {
	for msg := range h.broadcast {
		h.mu.RLock()
		for client := range h.clients {
			select {
			case client.send <- msg:
			default:
				// 客户端接收通道阻塞堆积，主动关闭剔除以防阻塞全局广播
				close(client.send)
				delete(h.clients, client)
			}
		}
		h.mu.RUnlock()
	}
}

// Broadcast 异步将事件广播给所有在线客户端
func (h *WSHub) Broadcast(data interface{}) {
	bytes, err := json.Marshal(data)
	if err != nil {
		return
	}
	select {
	case h.broadcast <- bytes:
	default:
		// 广播主队列满时丢弃，杜绝死锁
	}
}

// WritePump 独立的客户端写泵（确保每个连接串行写入）
func (c *WSClient) WritePump() {
	defer func() {
		c.hub.mu.Lock()
		delete(c.hub.clients, c)
		c.hub.mu.Unlock()
		c.conn.Close()
	}()

	for msg := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			break
		}
	}
}

// ReadPump 独立的客户端读泵
func (c *WSClient) ReadPump() {
	defer func() {
		c.hub.mu.Lock()
		delete(c.hub.clients, c)
		c.hub.mu.Unlock()
		c.conn.Close()
	}()

	c.conn.SetReadLimit(4096)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}
