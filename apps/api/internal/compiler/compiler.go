// Package compiler turns the desired-state model (a fleet's proxy hosts
// and their middleware chains) into Traefik dynamic configuration JSON.
//
// Output is deterministic: keys are produced in stable order so that two
// runs over identical input yield byte-identical output. Tests in
// testdata/ rely on this for golden-file comparison.
package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/danielfree19/proxydock/apps/api/internal/model"
)

// Result is a compiled revision payload, ready to be persisted and
// served to agents.
type Result struct {
	// Config is the Traefik dynamic configuration JSON.
	Config json.RawMessage
	// ETag identifies the compiled output so agents can short-circuit
	// matching `If-None-Match` requests.
	ETag string
}

// supportedMiddlewareTypes is the list of middleware types the compiler
// knows how to emit. Each maps 1:1 to a Traefik built-in.
var supportedMiddlewareTypes = map[string]bool{
	"headers":        true,
	"redirectScheme": true,
	"stripPrefix":    true,
	"basicAuth":      true,
	"forwardAuth":    true,
	"rateLimit":      true,
	"ipAllowList":    true,
	"retry":          true,
	"compress":       true,
	"circuitBreaker": true,
	"chain":          true,
}

// Compile takes a fleet's proxy hosts plus its certificate pool and
// returns the rendered Traefik dynamic config plus an ETag.
//
// Disabled hosts are dropped silently. The output always contains an
// `http` section even if the fleet has no enabled hosts; this avoids
// the provider rejecting an "empty" payload. Certificates are emitted
// into a top-level `tls.certificates` pool — Traefik picks the matching
// one for each TLS-enabled router via SNI.
func Compile(hosts []model.ProxyHost, certs []model.Certificate) (Result, error) {
	if err := Validate(hosts); err != nil {
		return Result{}, err
	}

	enabled := make([]model.ProxyHost, 0, len(hosts))
	for _, h := range hosts {
		if h.Enabled {
			enabled = append(enabled, h)
		}
	}
	// Stable order in: stable order out.
	sort.Slice(enabled, func(i, j int) bool { return enabled[i].Name < enabled[j].Name })

	out := map[string]any{}

	// http section is emitted unconditionally even when empty: the
	// provider plugin rejects responses whose `config` is `{}`, so we
	// always need at least one populated key. This was the contract
	// before TCP/UDP existed and we keep it.
	httpSection, err := compileHTTP(filterByProtocol(enabled, "http", ""))
	if err != nil {
		return Result{}, err
	}
	out["http"] = httpSection

	if tcp := compileTCP(filterByProtocol(enabled, "tcp")); tcp != nil {
		out["tcp"] = tcp
	}
	if udp := compileUDP(filterByProtocol(enabled, "udp")); udp != nil {
		out["udp"] = udp
	}
	if tls := renderCertPool(certs); tls != nil {
		out["tls"] = tls
	}

	body, err := marshalDeterministic(out)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(body)
	return Result{
		Config: body,
		ETag:   `"sha256-` + hex.EncodeToString(sum[:8]) + `"`,
	}, nil
}

// filterByProtocol returns a new slice of hosts whose Protocol matches
// any of the supplied values. Empty Protocol counts as "http" for
// backward compat with rows from before migration 009.
func filterByProtocol(hosts []model.ProxyHost, protocols ...string) []model.ProxyHost {
	keep := make(map[string]bool, len(protocols))
	for _, p := range protocols {
		keep[p] = true
	}
	out := make([]model.ProxyHost, 0, len(hosts))
	for _, h := range hosts {
		p := h.Protocol
		if p == "" {
			p = "http"
		}
		if keep[p] {
			out = append(out, h)
		}
	}
	return out
}

// compileHTTP renders the http.routers / http.services / http.middlewares
// section from the HTTP-protocol hosts. Returns an error if any
// middleware fails to render — Validate catches type-level problems
// at the boundary, but renderMiddleware also rejects malformed
// per-type config (e.g. stripPrefix without `prefixes`).
func compileHTTP(hosts []model.ProxyHost) (map[string]any, error) {
	routers := make(map[string]any, len(hosts))
	services := make(map[string]any, len(hosts))
	middlewares := map[string]any{}

	for _, h := range hosts {
		routerName := sanitizeName(h.Name)
		serviceName := routerName

		mwNames := make([]string, 0, len(h.Middlewares))
		// nameMap maps the operator's raw middleware name to the
		// per-host mangled name. Used to resolve `chain` references
		// after all middlewares on the host have been rendered.
		nameMap := make(map[string]string, len(h.Middlewares))
		for i, mw := range h.Middlewares {
			mwName := fmt.Sprintf("%s-%d-%s", routerName, i, sanitizeName(mw.Name))
			rendered, err := renderMiddleware(mw)
			if err != nil {
				return nil, fmt.Errorf("proxy host %q middleware %q: %w", h.Name, mw.Name, err)
			}
			middlewares[mwName] = rendered
			mwNames = append(mwNames, mwName)
			nameMap[mw.Name] = mwName
		}
		// Resolve `chain` middleware references on this host: rewrite
		// chain.middlewares from raw → mangled names.
		for i, mw := range h.Middlewares {
			if mw.Type != "chain" {
				continue
			}
			mwName := mwNames[i]
			outer, ok := middlewares[mwName].(map[string]any)
			if !ok {
				continue
			}
			inner, ok := outer["chain"].(map[string]any)
			if !ok {
				continue
			}
			refs, _ := inner["middlewares"].([]string)
			resolved := make([]string, 0, len(refs))
			for _, ref := range refs {
				mangled, ok := nameMap[ref]
				if !ok {
					return nil, fmt.Errorf("proxy host %q middleware %q: chain references unknown middleware %q", h.Name, mw.Name, ref)
				}
				resolved = append(resolved, mangled)
			}
			inner["middlewares"] = resolved
		}

		router := map[string]any{
			"rule":        fmt.Sprintf("Host(`%s`)", h.Domain),
			"entryPoints": orWeb(h.EntryPoints),
			"service":     serviceName,
		}
		if len(mwNames) > 0 {
			router["middlewares"] = mwNames
		}
		if h.TLS {
			// Empty object opts the router into TLS without forcing a
			// specific resolver/options; Traefik picks the cert from
			// the pool below by SNI.
			router["tls"] = map[string]any{}
		}
		routers[routerName] = router

		services[serviceName] = renderHTTPService(h)
	}

	httpSection := map[string]any{
		"routers":  routers,
		"services": services,
	}
	if len(middlewares) > 0 {
		httpSection["middlewares"] = middlewares
	}
	return httpSection, nil
}

// compileTCP renders the tcp.routers / tcp.services section from
// TCP-protocol hosts. Returns nil when the input is empty so the
// caller can omit the section entirely.
//
// Domain semantics for TCP:
//   - "*"  → HostSNI(`*`) catch-all.
//   - other → HostSNI(`<value>`); the connection's SNI must match.
//
// TLS termination is opt-in via h.TLS; when false the connection is
// forwarded as raw bytes (SNI passthrough).
func compileTCP(hosts []model.ProxyHost) map[string]any {
	if len(hosts) == 0 {
		return nil
	}
	routers := make(map[string]any, len(hosts))
	services := make(map[string]any, len(hosts))
	for _, h := range hosts {
		routerName := sanitizeName(h.Name)
		serviceName := routerName

		rule := fmt.Sprintf("HostSNI(`%s`)", h.Domain)
		router := map[string]any{
			"rule":        rule,
			"entryPoints": orWebsecure(h.EntryPoints),
			"service":     serviceName,
		}
		if h.TLS {
			router["tls"] = map[string]any{}
		}
		routers[routerName] = router

		services[serviceName] = renderL4Service(h)
	}
	return map[string]any{
		"routers":  routers,
		"services": services,
	}
}

// renderL4Service emits TCP/UDP service entries with one address per
// upstream URL (Phase 7 multi-upstream).
func renderL4Service(h model.ProxyHost) map[string]any {
	urls := effectiveUpstreamURLs(h)
	servers := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		servers = append(servers, map[string]any{"address": tcpAddress(u)})
	}
	return map[string]any{
		"loadBalancer": map[string]any{"servers": servers},
	}
}

// compileUDP is the simplest section: UDP routers have no rule, just
// entry points + service. Each UDP entry point may host one router;
// Validate enforces uniqueness so the compiler can stay dumb.
func compileUDP(hosts []model.ProxyHost) map[string]any {
	if len(hosts) == 0 {
		return nil
	}
	routers := make(map[string]any, len(hosts))
	services := make(map[string]any, len(hosts))
	for _, h := range hosts {
		routerName := sanitizeName(h.Name)
		serviceName := routerName
		routers[routerName] = map[string]any{
			"entryPoints": h.EntryPoints, // required for UDP — Validate ensures non-empty
			"service":     serviceName,
		}
		services[serviceName] = renderL4Service(h)
	}
	return map[string]any{
		"routers":  routers,
		"services": services,
	}
}

// tcpAddress strips a `tcp://` or `udp://` prefix from an upstream
// string, leaving the bare host:port that Traefik's TCP/UDP services
// expect. HTTP-style URLs aren't valid here; Validate rejects them.
func tcpAddress(s string) string {
	for _, prefix := range []string{"tcp://", "udp://"} {
		if strings.HasPrefix(s, prefix) {
			return strings.TrimPrefix(s, prefix)
		}
	}
	return s
}

// orWebsecure mirrors orWeb but defaults to "websecure" because TCP
// routers are most often paired with TLS termination on a TLS entry
// point. Operators who want plain TCP set entry_points explicitly.
func orWebsecure(ep []string) []string {
	if len(ep) == 0 {
		return []string{"websecure"}
	}
	out := make([]string, len(ep))
	copy(out, ep)
	return out
}

// renderCertPool emits `tls.certificates` from the fleet's cert pool.
//
// Traefik's FileOrContent type accepts either a path or the actual PEM
// content; passing the inline PEM lets agents apply certs without
// having to write them to disk first.
func renderCertPool(certs []model.Certificate) map[string]any {
	if len(certs) == 0 {
		return nil
	}
	// Sort so the output is deterministic.
	sorted := append([]model.Certificate(nil), certs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	out := make([]map[string]any, 0, len(sorted))
	for _, c := range sorted {
		out = append(out, map[string]any{
			"certFile": c.CertPEM,
			"keyFile":  c.KeyPEM,
		})
	}
	return map[string]any{"certificates": out}
}

// effectiveUpstreamURLs returns the host's upstream list, falling
// back to the legacy single-URL field when the array is empty.
func effectiveUpstreamURLs(h model.ProxyHost) []string {
	if len(h.UpstreamURLs) > 0 {
		out := make([]string, 0, len(h.UpstreamURLs))
		for _, u := range h.UpstreamURLs {
			if strings.TrimSpace(u) != "" {
				out = append(out, u)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if strings.TrimSpace(h.UpstreamURL) != "" {
		return []string{h.UpstreamURL}
	}
	return nil
}

// renderHTTPService emits the http.services entry for one proxy host.
// Phase 7: supports multiple servers, sticky sessions, and an
// optional healthCheck block.
func renderHTTPService(h model.ProxyHost) map[string]any {
	urls := effectiveUpstreamURLs(h)
	servers := make([]map[string]any, 0, len(urls))
	for _, u := range urls {
		servers = append(servers, map[string]any{"url": u})
	}
	lb := map[string]any{"servers": servers}
	if h.StickySession {
		// Empty cookie object opts into Traefik's default cookie
		// configuration. Operators who want a custom cookie name /
		// secure flag can set it via Traefik's static config; we
		// don't expose it on the proxy host yet because most
		// operators want sensible defaults.
		lb["sticky"] = map[string]any{"cookie": map[string]any{}}
	}
	if hc := healthCheckBlock(h); hc != nil {
		lb["healthCheck"] = hc
	}
	return map[string]any{"loadBalancer": lb}
}

// healthCheckBlock returns the Traefik healthCheck config for a host,
// or nil when the operator hasn't configured one. Path is the gating
// field — without it Traefik can't probe.
func healthCheckBlock(h model.ProxyHost) map[string]any {
	if len(h.HealthCheck) == 0 {
		return nil
	}
	path, _ := h.HealthCheck["path"].(string)
	if strings.TrimSpace(path) == "" {
		return nil
	}
	out := map[string]any{"path": path}
	for _, key := range []string{"interval", "timeout", "scheme", "hostname"} {
		if v, ok := h.HealthCheck[key]; ok {
			if s, isStr := v.(string); isStr && s != "" {
				out[key] = s
			}
		}
	}
	if v, ok := h.HealthCheck["port"]; ok {
		if n, isInt := toInt(v); isInt && n > 0 {
			out["port"] = n
		}
	}
	if v, ok := h.HealthCheck["followRedirects"].(bool); ok {
		out["followRedirects"] = v
	}
	return out
}

// renderMiddleware translates one model.Middleware into the
// Traefik dynamic-config shape, e.g.
//
//	{ "headers": {"customRequestHeaders": { ... }} }
func renderMiddleware(mw model.Middleware) (any, error) {
	switch mw.Type {
	case "headers":
		// pass through whatever the user supplied; Traefik validates it.
		return map[string]any{"headers": mw.Config}, nil

	case "redirectScheme":
		scheme, _ := mw.Config["scheme"].(string)
		if scheme == "" {
			scheme = "https"
		}
		body := map[string]any{"scheme": scheme}
		if v, ok := mw.Config["permanent"]; ok {
			body["permanent"] = v
		}
		if v, ok := mw.Config["port"]; ok {
			body["port"] = fmt.Sprint(v)
		}
		return map[string]any{"redirectScheme": body}, nil

	case "stripPrefix":
		prefixesRaw, ok := mw.Config["prefixes"]
		if !ok {
			return nil, errors.New("stripPrefix requires 'prefixes'")
		}
		prefixes, err := toStringSlice(prefixesRaw)
		if err != nil {
			return nil, fmt.Errorf("stripPrefix.prefixes: %w", err)
		}
		return map[string]any{"stripPrefix": map[string]any{"prefixes": prefixes}}, nil

	case "basicAuth":
		usersRaw, ok := mw.Config["users"]
		if !ok {
			return nil, errors.New("basicAuth requires 'users'")
		}
		users, err := toStringSlice(usersRaw)
		if err != nil {
			return nil, fmt.Errorf("basicAuth.users: %w", err)
		}
		return map[string]any{"basicAuth": map[string]any{"users": users}}, nil

	case "forwardAuth":
		// forwardAuth has many tunables (trustForwardHeader,
		// authResponseHeaders, authRequestHeaders, addAuthCookies, tls
		// etc.). We pass the operator's config object through verbatim
		// the way `headers` does and only enforce that `address` is
		// present — Traefik validates the rest.
		addr, _ := mw.Config["address"].(string)
		if strings.TrimSpace(addr) == "" {
			return nil, errors.New("forwardAuth requires 'address'")
		}
		return map[string]any{"forwardAuth": mw.Config}, nil

	case "rateLimit":
		// Traefik requires at least one of average / burst to be a
		// positive integer; otherwise the middleware is a no-op.
		_, hasAvg := mw.Config["average"]
		_, hasBurst := mw.Config["burst"]
		if !hasAvg && !hasBurst {
			return nil, errors.New("rateLimit requires 'average' or 'burst'")
		}
		return map[string]any{"rateLimit": mw.Config}, nil

	case "ipAllowList":
		rangesRaw, ok := mw.Config["sourceRange"]
		if !ok {
			return nil, errors.New("ipAllowList requires 'sourceRange'")
		}
		ranges, err := toStringSlice(rangesRaw)
		if err != nil {
			return nil, fmt.Errorf("ipAllowList.sourceRange: %w", err)
		}
		if len(ranges) == 0 {
			return nil, errors.New("ipAllowList.sourceRange must not be empty")
		}
		// We don't parse CIDRs here — Traefik does and surfaces a clear
		// error in its own logs. Validating client-side just doubles the
		// regex maintenance with no extra signal.
		return map[string]any{"ipAllowList": map[string]any{"sourceRange": ranges}}, nil

	case "retry":
		attempts, ok := toInt(mw.Config["attempts"])
		if !ok || attempts <= 0 {
			return nil, errors.New("retry requires 'attempts' as a positive integer")
		}
		body := map[string]any{"attempts": attempts}
		if v, ok := mw.Config["initialInterval"].(string); ok && v != "" {
			body["initialInterval"] = v
		}
		return map[string]any{"retry": body}, nil

	case "compress":
		// `compress` works as a no-op default; pass any tunables
		// (excludedContentTypes, minResponseBodyBytes) through verbatim.
		return map[string]any{"compress": mw.Config}, nil

	case "circuitBreaker":
		expr, _ := mw.Config["expression"].(string)
		if strings.TrimSpace(expr) == "" {
			return nil, errors.New("circuitBreaker requires 'expression'")
		}
		return map[string]any{"circuitBreaker": map[string]any{"expression": expr}}, nil

	case "chain":
		// chain references other middleware names defined on the same
		// proxy host (after the per-host name-prefix mangling we apply).
		// We emit raw names; the caller (compileHTTP) resolves them
		// against the host's middleware list.
		mwsRaw, ok := mw.Config["middlewares"]
		if !ok {
			return nil, errors.New("chain requires 'middlewares'")
		}
		names, err := toStringSlice(mwsRaw)
		if err != nil {
			return nil, fmt.Errorf("chain.middlewares: %w", err)
		}
		if len(names) == 0 {
			return nil, errors.New("chain.middlewares must not be empty")
		}
		return map[string]any{"chain": map[string]any{"middlewares": names}}, nil

	default:
		return nil, fmt.Errorf("unsupported middleware type %q", mw.Type)
	}
}

// ValidateMiddlewares checks the middleware list of a single proxy
// host (or a template) for shape errors that the per-host compile
// path would later trip on. It exercises:
//
//   - each middleware has a supported `type`
//   - each middleware renders cleanly (renderMiddleware does the
//     per-type required-field checks)
//   - any `chain` middleware references middleware names that exist
//     on the same list
//
// Returns the first error encountered. Used by the proxy host create /
// update and middleware template create / update handlers so we don't
// admit configs that would fail compilation later.
func ValidateMiddlewares(mws []model.Middleware) error {
	seen := make(map[string]bool, len(mws))
	for _, mw := range mws {
		if !supportedMiddlewareTypes[mw.Type] {
			return fmt.Errorf("unsupported middleware type %q (name=%s)", mw.Type, mw.Name)
		}
		if _, err := renderMiddleware(mw); err != nil {
			return fmt.Errorf("middleware %q: %w", mw.Name, err)
		}
		seen[mw.Name] = true
	}
	for _, mw := range mws {
		if mw.Type != "chain" {
			continue
		}
		refsRaw, _ := mw.Config["middlewares"]
		refs, _ := toStringSlice(refsRaw)
		for _, ref := range refs {
			if !seen[ref] {
				return fmt.Errorf("middleware %q: chain references unknown middleware %q", mw.Name, ref)
			}
		}
	}
	return nil
}

// Validate runs structural checks across all hosts before compilation,
// so callers can surface a list of errors rather than failing on the
// first bad row.
func Validate(hosts []model.ProxyHost) error {
	var errs []string
	seenNames := map[string]bool{}
	// Per-protocol domain uniqueness — an HTTP and a TCP host can
	// share a name like "api.example.com" because they live on
	// different routers (different rule types).
	seenHTTPDomains := map[string]string{}
	seenTCPDomains := map[string]string{}
	// UDP routers are matched only by entry point. Two UDP routers
	// on the same entry point would collide; track them.
	seenUDPEntryPoints := map[string]string{}

	for _, h := range hosts {
		if strings.TrimSpace(h.Name) == "" {
			errs = append(errs, "proxy host has empty name")
			continue
		}
		if seenNames[h.Name] {
			errs = append(errs, fmt.Sprintf("duplicate proxy host name %q", h.Name))
		}
		seenNames[h.Name] = true

		if !h.Enabled {
			continue
		}

		proto := h.Protocol
		if proto == "" {
			proto = "http"
		}
		switch proto {
		case "http":
			validateHTTP(h, seenHTTPDomains, &errs)
		case "tcp":
			validateTCP(h, seenTCPDomains, &errs)
		case "udp":
			validateUDP(h, seenUDPEntryPoints, &errs)
		default:
			errs = append(errs, fmt.Sprintf("%s: unsupported protocol %q (must be http, tcp, or udp)",
				h.Name, proto))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("compile validation failed:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func validateHTTP(h model.ProxyHost, seen map[string]string, errs *[]string) {
	if !looksLikeDomain(h.Domain) {
		*errs = append(*errs, fmt.Sprintf("%s: invalid domain %q", h.Name, h.Domain))
	}
	if other, ok := seen[h.Domain]; ok {
		*errs = append(*errs, fmt.Sprintf("%s: domain %q already used by %q", h.Name, h.Domain, other))
	} else {
		seen[h.Domain] = h.Name
	}
	urls := effectiveUpstreamURLs(h)
	if len(urls) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s: at least one upstream URL is required", h.Name))
	}
	scheme := ""
	for _, u := range urls {
		if err := checkUpstream(u); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: upstream %q: %v", h.Name, u, err))
			continue
		}
		// Mixed http/https is rejected: Traefik picks one scheme per
		// service and the result is non-deterministic for the user.
		s := "http"
		if strings.HasPrefix(u, "https://") {
			s = "https"
		}
		if scheme == "" {
			scheme = s
		} else if scheme != s {
			*errs = append(*errs, fmt.Sprintf("%s: upstream URLs mix http and https — pick one scheme", h.Name))
			break
		}
	}
	for _, mw := range h.Middlewares {
		if !supportedMiddlewareTypes[mw.Type] {
			*errs = append(*errs, fmt.Sprintf("%s: unsupported middleware type %q (name=%s)",
				h.Name, mw.Type, mw.Name))
		}
	}
}

func validateTCP(h model.ProxyHost, seen map[string]string, errs *[]string) {
	// TCP allows a "*" wildcard for the catch-all router; otherwise
	// require a domain that survives the same hostname checks HTTP uses.
	if h.Domain != "*" && !looksLikeDomain(h.Domain) {
		*errs = append(*errs, fmt.Sprintf("%s: invalid TCP domain %q (use \"*\" for catch-all)",
			h.Name, h.Domain))
	}
	if other, ok := seen[h.Domain]; ok {
		*errs = append(*errs, fmt.Sprintf("%s: TCP HostSNI %q already used by %q", h.Name, h.Domain, other))
	} else {
		seen[h.Domain] = h.Name
	}
	urls := effectiveUpstreamURLs(h)
	if len(urls) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s: at least one upstream is required", h.Name))
	}
	for _, u := range urls {
		if err := checkTCPUpstream(u); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: upstream %q: %v", h.Name, u, err))
		}
	}
	if len(h.Middlewares) > 0 {
		*errs = append(*errs, fmt.Sprintf("%s: middlewares are not supported on TCP routers", h.Name))
	}
}

func validateUDP(h model.ProxyHost, seenEPs map[string]string, errs *[]string) {
	if len(h.EntryPoints) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s: UDP routers require at least one entry_point",
			h.Name))
	}
	for _, ep := range h.EntryPoints {
		if other, ok := seenEPs[ep]; ok {
			*errs = append(*errs, fmt.Sprintf("%s: UDP entry_point %q already used by %q",
				h.Name, ep, other))
		} else {
			seenEPs[ep] = h.Name
		}
	}
	urls := effectiveUpstreamURLs(h)
	if len(urls) == 0 {
		*errs = append(*errs, fmt.Sprintf("%s: at least one upstream is required", h.Name))
	}
	for _, u := range urls {
		if err := checkTCPUpstream(u); err != nil {
			*errs = append(*errs, fmt.Sprintf("%s: upstream %q: %v", h.Name, u, err))
		}
	}
	if h.TLS {
		*errs = append(*errs, fmt.Sprintf("%s: UDP routers cannot use TLS", h.Name))
	}
	if len(h.Middlewares) > 0 {
		*errs = append(*errs, fmt.Sprintf("%s: middlewares are not supported on UDP routers",
			h.Name))
	}
}

// checkTCPUpstream accepts host:port (with an optional `tcp://` /
// `udp://` scheme prefix) — the format Traefik's TCP/UDP services
// expect for backend addresses. HTTP-style URLs are rejected so the
// rendered config doesn't silently produce invalid Traefik state.
func checkTCPUpstream(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return errors.New("required")
	}
	for _, prefix := range []string{"tcp://", "udp://"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			break
		}
	}
	if strings.Contains(s, "://") {
		return errors.New("must be host:port, not a URL")
	}
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return fmt.Errorf("must be host:port: %w", err)
	}
	if host == "" {
		return errors.New("missing host")
	}
	if port == "" {
		return errors.New("missing port")
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid port %q", port)
	}
	return nil
}

func checkUpstream(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q must be http or https", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("missing host")
	}
	return nil
}

func looksLikeDomain(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 253 {
		return false
	}
	if strings.ContainsAny(s, " /\\") {
		return false
	}
	for _, label := range strings.Split(s, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
				return false
			}
		}
	}
	return true
}

func sanitizeName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '-' || r == '_':
			b.WriteRune(r)
		case (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z'):
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func orWeb(ep []string) []string {
	if len(ep) == 0 {
		return []string{"web"}
	}
	out := make([]string, len(ep))
	copy(out, ep)
	return out
}

// toInt accepts the few JSON-decoded shapes a number can land as
// (float64 from generic decode, json.Number, an explicit int) and
// returns it as an int. Returns false if the value isn't numeric or
// has a fractional part.
func toInt(v any) (int, bool) {
	switch t := v.(type) {
	case float64:
		if t != float64(int(t)) {
			return 0, false
		}
		return int(t), true
	case int:
		return t, true
	case int64:
		return int(t), true
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

func toStringSlice(v any) ([]string, error) {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, el := range t {
			s, ok := el.(string)
			if !ok {
				return nil, fmt.Errorf("expected string, got %T", el)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return append([]string(nil), t...), nil
	default:
		return nil, fmt.Errorf("expected list of strings, got %T", v)
	}
}

// marshalDeterministic encodes a value as JSON with sorted map keys.
//
// Go's json.Marshal already sorts map[string]X keys alphabetically, but
// only for that specific type. Nested map[string]any values also sort.
// We re-encode through Marshal to enforce that behavior end-to-end.
func marshalDeterministic(v any) ([]byte, error) {
	return json.Marshal(v)
}
