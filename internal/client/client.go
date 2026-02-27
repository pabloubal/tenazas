// Package client defines the strategy interface for coding-agent backends
// and provides concrete implementations for each supported client.
package client

import "fmt"

// Client is the strategy interface every coding-agent backend must implement.
type Client interface {
	// Name returns the client identifier (e.g. "gemini", "claude-code").
	Name() string

	// Run executes a prompt and streams results.
	// nativeSID is the client-specific session ID for continuity.
	// onChunk is called with each text chunk as it streams.
	// onSessionID is called when the client provides its native session ID.
	Run(nativeSID, prompt, cwd, approvalMode string, yolo bool,
		onChunk func(string), onSessionID func(string)) (fullResponse string, err error)
}

// registry maps client names to constructor functions.
var registry = map[string]func(binPath, logPath string) Client{}

// Register adds a client constructor to the global registry.
func Register(name string, ctor func(binPath, logPath string) Client) {
	registry[name] = ctor
}

// NewClient creates a Client by name using the global registry.
func NewClient(name, binPath, logPath string) (Client, error) {
	ctor, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("unknown client: %q", name)
	}
	return ctor(binPath, logPath), nil
}

// RegisteredClients returns the names of all registered clients.
func RegisteredClients() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	return names
}
