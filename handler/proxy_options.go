package handler

import (
	"fmt"
	"time"

	"github.com/docker/go-units"
	"github.com/hashicorp/hcl/v2"

	"github.com/avenga/couper/config"
)

type ProxyOptions struct {
	ConnectTimeout, Timeout, TTFBTimeout time.Duration
	Context                              []hcl.Body
	BackendName                          string
	Hostname, Origin, Path, OpenAPIFile  string
	ValidateReq, ValidateRes             bool
	CORS                                 *CORSOptions
	RequestBodyLimit                     int64
}

func NewProxyOptions(conf *config.Backend, corsOpts *CORSOptions, remainCtx []hcl.Body) (*ProxyOptions, error) {
	totalD, err := time.ParseDuration(conf.Timeout)
	if err != nil {
		panic(err)
	}
	ttfbD, err := time.ParseDuration(conf.TTFBTimeout)
	if err != nil {
		panic(err)
	}
	connectD, err := time.ParseDuration(conf.ConnectTimeout)
	if err != nil {
		panic(err)
	}

	bodyLimit, err := units.FromHumanSize(conf.RequestBodyLimit)
	if err != nil {
		return nil, fmt.Errorf("backend bodyLimit: %v", err)
	}

	cors := corsOpts
	if cors == nil { // Could be nil on non api context like 'free' endpoints or definitions.
		cors = &CORSOptions{}
	}

	return &ProxyOptions{
		BackendName:      conf.Name,
		CORS:             cors,
		ConnectTimeout:   connectD,
		Context:          remainCtx,
		Hostname:         conf.Hostname,
		Origin:           conf.Origin,
		Path:             conf.Path,
		RequestBodyLimit: bodyLimit,
		TTFBTimeout:      ttfbD,
		Timeout:          totalD,
		OpenAPIFile:      conf.OpenAPIFile,
		ValidateReq:      conf.ValidateReq,
		ValidateRes:      conf.ValidateRes,
	}, nil
}

func (po *ProxyOptions) Merge(o *ProxyOptions) *ProxyOptions {
	if o.ConnectTimeout > 0 {
		po.ConnectTimeout = o.ConnectTimeout
	}

	if o.Timeout > 0 {
		po.Timeout = o.ConnectTimeout
	}

	if o.TTFBTimeout > 0 {
		po.TTFBTimeout = o.TTFBTimeout
	}

	if len(o.Context) > 0 {
		po.Context = append(po.Context, o.Context...)
	}

	if o.CORS != nil {
		po.CORS = o.CORS
	}

	if o.Hostname != "" {
		po.Hostname = o.Hostname
	}

	if o.Origin != "" {
		po.Origin = o.Origin
	}

	if o.Path != "" {
		po.Path = o.Path
	}

	if o.RequestBodyLimit != po.RequestBodyLimit {
		po.RequestBodyLimit = o.RequestBodyLimit
	}

	return po
}
