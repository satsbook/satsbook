package lnd

import (
	"sync"
)

// Pool maintains one LND client per node_id.
// It is safe for concurrent use.
type Pool struct {
	mu      sync.RWMutex
	clients map[int64]*Client
}

// NewPool returns an empty Pool.
func NewPool() *Pool {
	return &Pool{clients: make(map[int64]*Client)}
}

// Set stores a client for the given nodeID, replacing any previous one.
func (p *Pool) Set(nodeID int64, client *Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.clients[nodeID] = client
}

// Get returns the client for the given nodeID, and whether it was found.
func (p *Pool) Get(nodeID int64) (*Client, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	c, ok := p.clients[nodeID]
	return c, ok
}

// Remove removes and closes the client for the given nodeID.
// It is a no-op if the nodeID is not in the pool.
func (p *Pool) Remove(nodeID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[nodeID]; ok {
		c.Close()
		delete(p.clients, nodeID)
	}
}

// All returns a snapshot of all nodeID→client pairs currently in the pool.
func (p *Pool) All() map[int64]*Client {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make(map[int64]*Client, len(p.clients))
	for id, c := range p.clients {
		out[id] = c
	}
	return out
}
