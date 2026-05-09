package dns

import (
	"encoding/json"
	"fmt"
)

// Build constructs a Provider from a stored {type, config} pair.
//
// Each new DNS provider type adds a case here. Unknown types return an
// explicit error so the API surface can reject unsupported configs at
// the HTTP boundary.
func Build(typ string, config json.RawMessage) (Provider, error) {
	switch typ {
	case "pebble":
		return NewPebble([]byte(config))
	case "cloudflare":
		return NewCloudflare([]byte(config))
	case "route53":
		return NewRoute53([]byte(config))
	case "":
		return nil, fmt.Errorf("dns provider: type is required")
	default:
		return nil, fmt.Errorf("dns provider: unsupported type %q", typ)
	}
}
