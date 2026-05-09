// Package discovery enumerates upstream candidates from the
// environment so the UI can suggest them when an operator adds a
// proxy host.
//
// Phase 7 ships a Docker provider only — it asks the local Docker
// daemon for running containers and returns ones reachable from the
// manager's own network. Other providers (mDNS, k8s, file-based) can
// be added behind the same interface.
package discovery

import (
	"context"
	"errors"
	"fmt"
)

// Service is one upstream candidate.
type Service struct {
	// ID identifies the underlying source (e.g. Docker container ID,
	// short form). Opaque to the UI.
	ID string `json:"id"`
	// Name is the user-facing label (container name, hostname).
	Name string `json:"name"`
	// Image / source description (Docker image, k8s service kind, etc.).
	Image string `json:"image,omitempty"`
	// IP is the address reachable from Traefik agents on the shared
	// network. For the Docker provider this is the bridge-network IP.
	IP string `json:"ip"`
	// Network is the underlying network the IP lives on. For the
	// Docker provider this is the docker network name shared with the
	// manager-api container.
	Network string `json:"network,omitempty"`
	// Ports is the list of TCP ports the service exposes on `IP`.
	Ports []int `json:"ports,omitempty"`
}

// Provider implementations enumerate upstream candidates.
type Provider interface {
	// Name returns the provider key (e.g. "docker"). Used to surface
	// the active provider in API responses.
	Name() string
	// List returns every reachable upstream candidate.
	List(ctx context.Context) ([]Service, error)
}

// Build constructs the provider named by `kind`. Returns nil for the
// empty string (discovery disabled).
func Build(kind string) (Provider, error) {
	switch kind {
	case "":
		return nil, nil
	case "docker":
		return NewDockerProvider("/var/run/docker.sock")
	default:
		return nil, fmt.Errorf("unknown discovery provider %q", kind)
	}
}

// ErrUnavailable is the sentinel a Provider returns when it can talk
// to its source but has no useful answers (e.g. the docker socket
// exists but the daemon doesn't recognize the manager's container ID
// — which happens in some non-docker container runtimes that mount
// the socket from outside).
var ErrUnavailable = errors.New("discovery provider has no candidates")
