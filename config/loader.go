package config

import (
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsimple"
	"github.com/sirupsen/logrus"

	ac "go.avenga.cloud/couper/gateway/access_control"
	"go.avenga.cloud/couper/gateway/backend"
)

var typeMap = map[string]func(*logrus.Entry, hcl.Body) http.Handler{
	"proxy": backend.NewProxy(),
}

func LoadFile(name string, log *logrus.Entry) *Gateway {
	config := &Gateway{}
	err := hclsimple.DecodeFile(name, nil, config)
	if err != nil {
		log.Fatalf("Failed to load configuration: %s", err)
	}
	return Load(config, log)
}

func LoadBytes(src []byte, log *logrus.Entry) *Gateway {
	config := &Gateway{}
	// filename must match .hcl ending for further []byte processing
	if err := hclsimple.Decode("loadBytes.hcl", src, nil, config); err != nil {
		log.Fatalf("Failed to load configuration bytes: %s", err)
	}
	return Load(config, log)
}

func Load(config *Gateway, log *logrus.Entry) *Gateway {
	accessControls := configureAccessControls(config)

	backends := make(map[string]http.Handler)

	for idx, server := range config.Server {
		configureDomains(server)

		if server.Api == nil {
			continue
		}

		// create backends
		for _, be := range server.API.Backend {
			if isKeyword(be.Name) {
				log.Fatalf("be name not allowed, reserved keyword: '%s'", be.Name)
			}
			if _, ok := backends[be.Name]; ok {
				log.Fatalf("be name must be unique: '%s'", be.Name)
			}
			backends[be.Name] = newBackend(be.Kind, be.Options, log)
		}

		server.API.PathHandler = make(PathHandler)

		// map backends to endpoint
		endpoints := make(map[string]bool)
		for e, endpoint := range server.API.Endpoint {
			config.Server[idx].API.Endpoint[e].Server = server // assign parent
			if endpoints[endpoint.Pattern] {
				log.Fatal("Duplicate endpoint: ", endpoint.Pattern)
			}

			endpoints[endpoint.Pattern] = true

			var acList ac.List
			for _, acName := range endpoint.AccessControl {
				if _, ok := accessControls[acName]; !ok {
					log.Fatalf("access control %q not found", acName)
				}
				acList = append(acList, accessControls[acName])
			}

			if endpoint.Backend != "" {
				if _, ok := backends[endpoint.Backend]; !ok {
					log.Fatalf("backend %q is not defined", endpoint.Backend)
				}
				server.API.PathHandler[endpoint] = NewHandlerWrapper(log, acList, backends[endpoint.Backend])
				continue
			}

			content, leftOver, diags := endpoint.Options.PartialContent(server.API.Schema(true))
			if diags.HasErrors() {
				log.Fatal(diags.Error())
			}
			endpoint.Options = leftOver

			if content == nil || len(content.Blocks) == 0 {
				log.Fatalf("expected backend attribute reference or block for endpoint: %s", endpoint)
			}
			kind := content.Blocks[0].Labels[0]

			server.API.PathHandler[endpoint] = NewHandlerWrapper(log, acList, newBackend(kind, content.Blocks[0].Body, log)) // inline be
		}
	}

	return config
}

// configureDomains is a fallback configuration which ensures
// the request multiplexer is working properly.
func configureDomains(server *Server) {
	if len(server.Domains) > 0 {
		return
	}
	// TODO: ipv6
	server.Domains = []string{"localhost", "127.0.0.1", "0.0.0.0"}
}

func newBackend(kind string, options hcl.Body, log *logrus.Entry) http.Handler {
	if !isKeyword(kind) {
		log.Fatalf("Invalid backend: %s", kind)
	}
	b := typeMap[strings.ToLower(kind)](log, options)

	return b
}

func isKeyword(other string) bool {
	_, yes := typeMap[other]
	return yes
}

func configureAccessControls(conf *Gateway) ac.Map {
	accessControls := make(ac.Map)
	if conf.Definitions != nil {
		for _, jwt := range conf.Definitions.JWT {
			var jwtSource ac.Source
			var jwtKey string
			if jwt.Cookie != "" {
				jwtSource = ac.Cookie
				jwtKey = jwt.Cookie
			} else if jwt.Header != "" {
				jwtSource = ac.Header
				jwtKey = jwt.Header
			}
			var key []byte
			if jwt.KeyFile != "" {
				wd, _ := os.Getwd()
				content, err := ioutil.ReadFile(path.Join(wd, jwt.KeyFile))
				if err != nil {
					panic(err)
				}
				key = content
			} else if jwt.Key != "" {
				key = []byte(jwt.Key)
			}
			j, err := ac.NewJWT(jwt.SignatureAlgorithm, jwtSource, jwtKey, key)
			if err != nil {
				panic(err)
			}
			accessControls[jwt.Name] = j
		}
	}
	return accessControls
}
