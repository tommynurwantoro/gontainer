package gontainer

import (
	"fmt"
	"log"
	"sync"

	"github.com/tommynurwantoro/gontainer/inject"
)

type Graph interface {
	Provide(objects ...*inject.Object) error
	Populate() error
}

type Service interface {
	Startup() error
	Shutdown() error
}

type Container interface {
	Ready() error
	GetServiceOrNil(id string) interface{}
	RegisterService(id string, svc interface{})
	Shutdown()
}

type container struct {
	mu       sync.RWMutex
	graph    Graph
	order    []string
	ready    bool
	services map[string]interface{}
}

func New() Container {
	return &container{
		graph:    new(inject.Graph),
		order:    make([]string, 0, 16),            // Pre-allocate with capacity hint
		services: make(map[string]interface{}, 16), // Pre-allocate with capacity hint
		ready:    false,
	}
}

// Ready starts up the service graph and returns error if it's not ready
func (c *container) Ready() error {
	c.mu.RLock()
	if c.ready {
		c.mu.RUnlock()
		return nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.ready {
		return nil
	}

	if err := c.graph.Populate(); err != nil {
		return fmt.Errorf("failed to populate graph: %w", err)
	}
	for _, key := range c.order {
		obj := c.services[key]
		if s, ok := obj.(Service); ok {
			log.Println("[starting up] ", key)
			if err := s.Startup(); err != nil {
				return fmt.Errorf("failed to start service %s: %w", key, err)
			}
		}
	}
	c.ready = true
	return nil
}

func (c *container) RegisterService(id string, svc interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ready {
		log.Printf("warning: registering service %s after container is ready", id)
	}

	err := c.graph.Provide(&inject.Object{Name: id, Value: svc, Complete: false})
	if err != nil {
		// Return error instead of panicking - but we can't change the interface
		// So we'll log and panic for backward compatibility, but with better error message
		log.Printf("error providing service %s: %v", id, err)
		panic(fmt.Errorf("failed to register service %s: %w", id, err))
	}
	c.order = append(c.order, id)
	c.services[id] = svc
}

func (c *container) GetServiceOrNil(id string) interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	svc, ok := c.services[id]
	if !ok {
		panic(fmt.Errorf("service %s not found", id))
	}
	return svc
}

func (c *container) Shutdown() {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range c.order {
		if service, ok := c.services[key]; ok {
			if s, ok := service.(Service); ok {
				log.Println("[shutting down] ", key)
				if err := s.Shutdown(); err != nil {
					log.Printf("ERROR: [shutting down] %s: %v", key, err)
				}
			}
		}
	}
	c.ready = false
}
