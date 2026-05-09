package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"time"
)

// DockerProvider talks to a local Docker daemon via its unix socket
// and returns running containers reachable on a network the manager
// is also attached to. We avoid the official SDK to keep dependencies
// minimal — the API surface used here is tiny and stable.
//
// Trust model: the docker socket is root-equivalent. This provider is
// intended for single-tenant, operator-managed deployments. Don't
// enable on multi-tenant setups.
type DockerProvider struct {
	client *http.Client
	// hostname is what Docker treats as the manager's own container
	// ID (Docker sets the container's HOSTNAME env to a 12-char prefix
	// of the full ID by default). We use it to look up our own
	// network membership and filter candidates to shared networks.
	hostname string
}

// NewDockerProvider returns a provider talking to the docker daemon
// on the supplied unix socket path.
func NewDockerProvider(socketPath string) (*DockerProvider, error) {
	if _, err := os.Stat(socketPath); err != nil {
		return nil, fmt.Errorf("docker socket %s: %w", socketPath, err)
	}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", socketPath)
		},
	}
	return &DockerProvider{
		client:   &http.Client{Transport: tr, Timeout: 5 * time.Second},
		hostname: os.Getenv("HOSTNAME"),
	}, nil
}

func (p *DockerProvider) Name() string { return "docker" }

// dockerContainer mirrors the subset of /containers/json we use.
type dockerContainer struct {
	ID    string   `json:"Id"`
	Names []string `json:"Names"`
	Image string   `json:"Image"`
	Ports []struct {
		PrivatePort int    `json:"PrivatePort"`
		PublicPort  int    `json:"PublicPort,omitempty"`
		Type        string `json:"Type"`
	} `json:"Ports"`
	NetworkSettings struct {
		Networks map[string]struct {
			IPAddress string `json:"IPAddress"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// List returns one Service per container × shared-network combination.
// Filters the manager's own container out (no point routing back to
// ourselves) and skips containers with no IP on any shared network.
func (p *DockerProvider) List(ctx context.Context) ([]Service, error) {
	managerNets, err := p.managerNetworks(ctx)
	if err != nil {
		return nil, err
	}
	containers, err := p.listContainers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Service, 0, len(containers))
	for _, c := range containers {
		if isSelf(c.ID, p.hostname) {
			continue
		}
		// Collect TCP ports only — UDP discovery would land in a
		// different upstream form (proxy_hosts.protocol = udp) and
		// we leave that for a follow-up.
		ports := make([]int, 0, len(c.Ports))
		seen := map[int]bool{}
		for _, port := range c.Ports {
			if port.Type != "tcp" || port.PrivatePort == 0 || seen[port.PrivatePort] {
				continue
			}
			ports = append(ports, port.PrivatePort)
			seen[port.PrivatePort] = true
		}
		sort.Ints(ports)
		// Emit one Service per network membership the manager shares;
		// the UI can pick the right one when a container is on
		// multiple networks (rare but possible).
		for netName, settings := range c.NetworkSettings.Networks {
			if managerNets != nil && !managerNets[netName] {
				continue
			}
			if settings.IPAddress == "" {
				continue
			}
			out = append(out, Service{
				ID:      shortID(c.ID),
				Name:    primaryName(c.Names),
				Image:   c.Image,
				IP:      settings.IPAddress,
				Network: netName,
				Ports:   ports,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (p *DockerProvider) listContainers(ctx context.Context) ([]dockerContainer, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker /containers/json: %s", resp.Status)
	}
	var cs []dockerContainer
	if err := json.NewDecoder(resp.Body).Decode(&cs); err != nil {
		return nil, fmt.Errorf("decode containers: %w", err)
	}
	return cs, nil
}

// managerNetworks returns the set of network names the manager-api
// container itself is attached to. We use HOSTNAME (= short container
// ID) as the lookup key — Docker accepts both short and full IDs at
// /containers/{id}/json. If the lookup fails (e.g. running outside a
// container during tests) we return nil, which tells List() to skip
// the network filter and return everything.
func (p *DockerProvider) managerNetworks(ctx context.Context) (map[string]bool, error) {
	if p.hostname == "" {
		return nil, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "http://docker/containers/"+p.hostname+"/json", nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		// Manager isn't a docker container — return nil so we don't
		// over-filter. Operators running this on baremetal still get
		// useful results.
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("docker /containers/%s/json: %s", p.hostname, resp.Status)
	}
	var info struct {
		NetworkSettings struct {
			Networks map[string]struct{} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(info.NetworkSettings.Networks))
	for name := range info.NetworkSettings.Networks {
		out[name] = true
	}
	return out, nil
}

func isSelf(containerID, hostname string) bool {
	if hostname == "" {
		return false
	}
	return len(containerID) >= len(hostname) && containerID[:len(hostname)] == hostname
}

func primaryName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	// Docker prefixes container names with "/". Strip it.
	n := names[0]
	if len(n) > 0 && n[0] == '/' {
		n = n[1:]
	}
	return n
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
