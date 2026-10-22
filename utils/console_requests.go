package utils

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"sync"

	"github.com/spf13/viper"
)

// Service console message protocol (/_sish/console/ws), ported from upstream
// 52b1114/86d6b30 and adapted to the fork's console:
//
//   - a request: a JSON object with a unique, increasing "id";
//   - a websocket event that belongs to an earlier request: a JSON object with
//     "type" (ws-message, ws-error, ws-close), "requestId" and a per-request
//     increasing "seq";
//   - the history sent once when a page connects: a JSON array of the above,
//     oldest first, which may overlap with live messages (pages dedupe by id
//     and seq);
//   - {"type":"dropped","count":n}: n live messages did not fit in the page's
//     queue and were skipped.

// consoleHistoryMaxBytes bounds the request history retained per route, so a
// route with large bodies can't grow the process without limit when
// service-console-max-content-length is -1. Oldest requests go first.
const consoleHistoryMaxBytes = 8 << 20

// consoleRecord is a request kept for consoles that connect later, with the
// websocket events that followed it.
type consoleRecord struct {
	id      uint64
	message []byte
	events  [][]byte
	size    int
}

// consoleRequestHistory holds the recent requests of every route.
type consoleRequestHistory struct {
	lock   sync.Mutex
	routes map[string][]*consoleRecord
}

// BroadcastRoute records a request made to a route and sends it to all of the
// route's clients. It returns the request's id, or 0 if the route has no
// console.
func (c *WebConsole) BroadcastRoute(route string, data map[string]any) uint64 {
	if !c.RouteExists(route) {
		return 0
	}

	id := c.requestID.Add(1)
	data["id"] = id

	message, err := json.Marshal(data)
	if err != nil {
		log.Println("Unable to marshal console data:", err)
		return 0
	}

	c.addRequestHistory(route, id, message)
	c.queueToRoute(route, message)

	return id
}

// BroadcastEvent sends an event that belongs to an earlier request, such as a
// websocket message, to the route's clients. It never blocks, as it runs on
// the tunnel's copy path.
func (c *WebConsole) BroadcastEvent(route string, requestID uint64, data map[string]any) {
	if !c.RouteExists(route) {
		return
	}

	data["requestId"] = requestID

	message, err := json.Marshal(data)
	if err != nil {
		log.Println("Unable to marshal console event:", err)
		return
	}

	c.addRequestEvent(route, requestID, message)
	c.queueToRoute(route, message)
}

// queueToRoute hands message to every client of route without waiting.
func (c *WebConsole) queueToRoute(route string, message []byte) {
	c.ClientsLock.RLock()
	clients, ok := c.Clients.Load(route)
	if ok {
		copied := make([]*WebClient, len(clients))
		copy(copied, clients)
		clients = copied
	}
	c.ClientsLock.RUnlock()

	for _, client := range clients {
		if client != nil {
			client.queue(message)
		}
	}
}

// requestHistoryFor returns the history store, creating it for a WebConsole
// built without NewWebConsole.
func (c *WebConsole) requestHistoryFor() *consoleRequestHistory {
	c.requestHistoryOnce.Do(func() {
		if c.requestHistory == nil {
			c.requestHistory = &consoleRequestHistory{routes: map[string][]*consoleRecord{}}
		}
	})

	return c.requestHistory
}

// addRequestHistory keeps the most recent requests for a route, up to
// service-console-history and consoleHistoryMaxBytes.
func (c *WebConsole) addRequestHistory(route string, id uint64, message []byte) {
	limit := viper.GetInt("service-console-history")
	if limit <= 0 || len(message) > consoleHistoryMaxBytes {
		return
	}

	h := c.requestHistoryFor()
	h.lock.Lock()
	defer h.lock.Unlock()

	// Checked under the lock, so a request that races RemoveRoute can't
	// leave history behind for a route that is gone.
	if !c.RouteExists(route) {
		return
	}

	records := h.routes[route]
	if len(records) >= limit {
		records = append(records[:0], records[len(records)-limit+1:]...)
	}

	h.routes[route] = append(records, &consoleRecord{id: id, message: message, size: len(message)})
	h.trim(route)
}

// addRequestEvent keeps an event with its request, up to
// service-console-websocket-messages per request.
func (c *WebConsole) addRequestEvent(route string, requestID uint64, message []byte) {
	limit := viper.GetInt("service-console-websocket-messages")
	if limit <= 0 || viper.GetInt("service-console-history") <= 0 {
		return
	}

	h := c.requestHistoryFor()
	h.lock.Lock()
	defer h.lock.Unlock()

	records := h.routes[route]
	for i := len(records) - 1; i >= 0; i-- {
		record := records[i]
		if record.id != requestID {
			continue
		}

		if len(record.events) >= limit {
			for _, evicted := range record.events[:len(record.events)-limit+1] {
				record.size -= len(evicted)
			}
			record.events = append(record.events[:0], record.events[len(record.events)-limit+1:]...)
		}

		record.events = append(record.events, message)
		record.size += len(message)
		h.trim(route)

		return
	}
}

// trim drops the oldest requests of route, then the oldest events of the only
// one left, until the route fits in consoleHistoryMaxBytes. Callers hold lock.
func (h *consoleRequestHistory) trim(route string) {
	records := h.routes[route]

	total := 0
	for _, record := range records {
		total += record.size
	}

	for total > consoleHistoryMaxBytes && len(records) > 1 {
		total -= records[0].size
		records = records[1:]
	}

	if total > consoleHistoryMaxBytes && len(records) == 1 {
		record := records[0]
		for total > consoleHistoryMaxBytes && len(record.events) > 0 {
			total -= len(record.events[0])
			record.size -= len(record.events[0])
			record.events = record.events[1:]
		}
	}

	h.routes[route] = records
}

// RequestHistory returns the recorded requests for a route, oldest first, each
// followed by its events.
func (c *WebConsole) RequestHistory(route string) [][]byte {
	h := c.requestHistoryFor()
	h.lock.Lock()
	defer h.lock.Unlock()

	var messages [][]byte
	for _, record := range h.routes[route] {
		messages = append(messages, record.message)
		messages = append(messages, record.events...)
	}

	return messages
}

// removeRequestHistory forgets the history of a route that is gone.
func (c *WebConsole) removeRequestHistory(route string) {
	h := c.requestHistoryFor()
	h.lock.Lock()
	delete(h.routes, route)
	h.lock.Unlock()
}

// sendRequestHistory sends the route's history to a page that just connected,
// as one batch so it can't fill the queue live messages use.
func (c *WebConsole) sendRequestHistory(route string, client *WebClient) {
	history := c.RequestHistory(route)
	if len(history) == 0 {
		return
	}

	batch := append([]byte("["), bytes.Join(history, []byte(","))...)
	batch = append(batch, ']')

	go client.send(batch)
}

// send passes a message to the client's writer, waiting for room. It returns
// false if the client has closed.
func (c *WebClient) send(message []byte) bool {
	select {
	case c.Send <- message:
		return true
	case <-c.Done:
		return false
	}
}

// queue passes a live message to the client's writer without waiting, so a
// slow page can't hold up the request or tunnel that produced it. Messages
// that don't fit are counted and reported to the page; a page that stops
// reading altogether is closed by the write deadline in Handle.
func (c *WebClient) queue(message []byte) {
	if c.closed.Load() {
		return
	}

	select {
	case c.Send <- message:
	default:
		c.dropped.Add(1)
	}
}

// droppedNotice returns the notice for messages dropped since the last call,
// or nil if there were none.
func (c *WebClient) droppedNotice() []byte {
	if n := c.dropped.Swap(0); n > 0 {
		return fmt.Appendf(nil, `{"type":"dropped","count":%d}`, n)
	}

	return nil
}
