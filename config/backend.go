package config

import (
	"github.com/hashicorp/hcl/v2"
)

type Backend struct {
	ConnectTimeout   string   `hcl:"connect_timeout,optional"`
	Hostname         string   `hcl:"hostname,optional"`
	Name             string   `hcl:"name,label"`
	Options          hcl.Body `hcl:",remain"`
	Origin           string   `hcl:"origin,optional"` // mixed, not required for overrides
	Path             string   `hcl:"path,optional"`
	RequestBodyLimit string   `hcl:"request_body_limit,optional"`
	TTFBTimeout      string   `hcl:"ttfb_timeout,optional"`
	Timeout          string   `hcl:"timeout,optional"`
	OpenAPIFile      string   `hcl:"openapi_file,optional"`
	ValidateReq      bool     `hcl:"validate_request,optional"`
	ValidateRes      bool     `hcl:"validate_response,optional"`
}

// Merge overrides the left backend configuration and returns a new instance.
func (b *Backend) Merge(other *Backend) (*Backend, []hcl.Body) {
	if b == nil || other == nil {
		return nil, nil
	}

	var bodies []hcl.Body

	result := *b

	if other.Hostname != "" {
		result.Hostname = other.Hostname
	}

	if other.Name != "" {
		result.Name = other.Name
	}

	if result.Options != nil {
		bodies = append(bodies, result.Options)
	}

	if other.Options != nil {
		bodies = append(bodies, other.Options)
		result.Options = other.Options
	}

	if other.Origin != "" {
		result.Origin = other.Origin
	}

	if other.Path != "" {
		result.Path = other.Path
	}

	if other.ConnectTimeout != "" {
		result.ConnectTimeout = other.ConnectTimeout
	}

	if other.RequestBodyLimit != "" {
		result.RequestBodyLimit = other.RequestBodyLimit
	}

	if other.TTFBTimeout != "" {
		result.TTFBTimeout = other.TTFBTimeout
	}

	if other.Timeout != "" {
		result.Timeout = other.Timeout
	}

	if other.OpenAPIFile != "" {
		result.OpenAPIFile = other.OpenAPIFile
	}

	if other.ValidateReq {
		result.ValidateReq = other.ValidateReq
	}

	if other.ValidateRes {
		result.ValidateRes = other.ValidateRes
	}

	return &result, bodies
}
