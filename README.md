# Gontainer

A modern, high-performance dependency injection container library for Go. Gontainer is an enhanced version of the archived [facebookarchive/inject](https://github.com/facebookarchive/inject) library, featuring improved performance, thread-safety, and modern Go best practices.

## Features

- **Dependency Injection**: Automatic dependency resolution using struct tags
- **Service Container**: Register and manage services with lifecycle support
- **Thread-Safe**: Built-in concurrency support for safe concurrent access
- **Multiple Injection Types**: Support for singletons, private instances, and named dependencies
- **Interface Injection**: Automatic interface implementation resolution
- **Lifecycle Management**: Startup and shutdown hooks for services

## Installation

```bash
go get -u github.com/tommynurwantoro/gontainer
```

## Quick Start

### Using the Container (Recommended)

The `Container` interface provides a high-level API for managing services:

```go
package main

import (
	"log"
	"github.com/tommynurwantoro/gontainer"
)

type Database struct {
	// Put object database here
}

type Service struct {
	DB *Database `inject:"service"`
}

func (s *Service) Startup() error {
	log.Println("Service started")
	return nil
}

func (s *Service) Shutdown() error {
	log.Println("Service stopped")
	return nil
}

func main() {
	container := gontainer.New()
	
	// Register services
	container.RegisterService("database", &Database{})
	container.RegisterService("service", &Service{})
	
	// Initialize and start all services
	if err := container.Ready(); err != nil {
		log.Fatal(err)
	}
	
	// Get a service
	svc := container.GetServiceOrNil("service").(*Service)
	
	// Cleanup
	container.Shutdown()
}
```

## Struct Tags

Gontainer uses struct tags to identify fields that should be injected. The tag format follows the standard Go struct tag conventions used by `json`, `xml`, etc.

### Singleton Injection (`inject:""`)

The most common pattern - creates a single shared instance:

```go
type Database struct{}

type Service struct {
	DB *Database `inject:""`  // Same instance shared across all services
}
```

### Private Instance (`inject:"private"`)

Creates a new instance for each injection point:

```go
type Config struct{}

type ServiceA struct {
	Config *Config `inject:"private"`  // New instance
}

type ServiceB struct {
	Config *Config `inject:"private"`  // Different instance
}
```

## Advanced Usage

### Service Lifecycle

Implement the `Service` interface to add startup/shutdown hooks:

```go
type MyService struct {
	DB *Database `inject:""`
}

func (s *MyService) Startup() error {
	// Called when container.Ready() is invoked
	return s.DB.Connect()
}

func (s *MyService) Shutdown() error {
	// Called when container.Shutdown() is invoked
	return s.DB.Close()
}
```

## How It Works

Gontainer uses Go's reflection package to analyze struct tags and automatically:
1. Creates instances of dependencies when needed
2. Resolves interface implementations
3. Injects dependencies into struct fields
4. Manages singleton instances across the object graph

**Note**: Since it uses reflection, Gontainer can only inject into exported (public) fields. Private fields cannot be injected.

## Performance

Gontainer includes several performance optimizations:
- Cached reflection operations
- Type-indexed dependency lookups
- Pre-allocated collections
- Efficient zero-value checks

## Examples

See the `_example` directory for complete working examples.

## Contributing

Contributions are welcome! Please follow the [Contribution Guidelines](CONTRIBUTION.md).

## License

This project is licensed under the MIT License.
