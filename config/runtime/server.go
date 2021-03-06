package runtime

import (
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/pathpattern"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/sirupsen/logrus"

	ac "github.com/avenga/couper/accesscontrol"
	"github.com/avenga/couper/config"
	"github.com/avenga/couper/config/runtime/server"
	"github.com/avenga/couper/errors"
	"github.com/avenga/couper/eval"
	"github.com/avenga/couper/handler"
	"github.com/avenga/couper/internal/seetie"
	"github.com/avenga/couper/utils"
)

var defaultBackendConf = &config.Backend{
	ConnectTimeout:   "10s",
	RequestBodyLimit: "64MiB",
	TTFBTimeout:      "60s",
	Timeout:          "300s",
}

var (
	errorMissingBackend = fmt.Errorf("no backend attribute reference or block")
	errorMissingServer  = fmt.Errorf("missing server definitions")
)

type backendDefinition struct {
	conf    *config.Backend
	handler http.Handler
}

type Port int

func (p Port) String() string {
	return strconv.Itoa(int(p))
}

type ServerConfiguration struct {
	PortOptions map[Port]*MuxOptions
}

type hosts map[string]bool
type ports map[Port]hosts

type HandlerKind uint8

const (
	KindAPI HandlerKind = iota
	KindFiles
	KindSPA
)

// NewServerConfiguration sets http handler specific defaults and validates the given gateway configuration.
// Wire up all endpoints and maps them within the returned Server.
func NewServerConfiguration(conf *config.Gateway, httpConf *HTTPConfig, log *logrus.Entry) (*ServerConfiguration, error) {
	if len(conf.Server) == 0 {
		return nil, errorMissingServer
	}

	// (arg && env) > conf
	defaultPort := conf.Settings.DefaultPort
	if httpConf.ListenPort != defaultPort {
		defaultPort = httpConf.ListenPort
	}

	// confCtx is created to evaluate request / response related configuration errors on start.
	noopReq := httptest.NewRequest(http.MethodGet, "https://couper.io", nil)
	noopResp := httptest.NewRecorder().Result()
	noopResp.Request = noopReq
	confCtx := eval.NewHTTPContext(conf.Context, 0, noopReq, noopReq, noopResp)

	validPortMap, hostsMap, err := validatePortHosts(conf, defaultPort)
	if err != nil {
		return nil, err
	}

	backends, err := newBackendsFromDefinitions(conf, confCtx, log)
	if err != nil {
		return nil, err
	}

	accessControls, err := configureAccessControls(conf, confCtx)
	if err != nil {
		return nil, err
	}

	serverConfiguration := &ServerConfiguration{PortOptions: map[Port]*MuxOptions{
		Port(defaultPort): NewMuxOptions(hostsMap)},
	}
	for p := range validPortMap {
		serverConfiguration.PortOptions[p] = NewMuxOptions(hostsMap)
	}

	api := make(map[*config.Endpoint]http.Handler)

	for _, srvConf := range conf.Server {
		serverOptions, err := server.NewServerOptions(srvConf)
		if err != nil {
			return nil, err
		}

		var spaHandler http.Handler
		if srvConf.Spa != nil {
			spaHandler, err = handler.NewSpa(srvConf.Spa.BootstrapFile, serverOptions)
			if err != nil {
				return nil, err
			}

			spaHandler = configureProtectedHandler(accessControls, serverOptions.ServerErrTpl,
				config.NewAccessControl(srvConf.AccessControl, srvConf.DisableAccessControl),
				config.NewAccessControl(srvConf.Spa.AccessControl, srvConf.Spa.DisableAccessControl), spaHandler)

			for _, spaPath := range srvConf.Spa.Paths {
				err = setRoutesFromHosts(serverConfiguration, defaultPort, srvConf.Hosts, path.Join(serverOptions.SPABasePath, spaPath), spaHandler, KindSPA)
				if err != nil {
					return nil, err
				}
			}
		}

		if srvConf.Files != nil {
			fileHandler, err := handler.NewFile(serverOptions.FileBasePath, srvConf.Files.DocumentRoot, serverOptions)
			if err != nil {
				return nil, err
			}

			protectedFileHandler := configureProtectedHandler(accessControls, serverOptions.FileErrTpl,
				config.NewAccessControl(srvConf.AccessControl, srvConf.DisableAccessControl),
				config.NewAccessControl(srvConf.Files.AccessControl, srvConf.Files.DisableAccessControl), fileHandler)

			err = setRoutesFromHosts(serverConfiguration, defaultPort, srvConf.Hosts, serverOptions.FileBasePath, protectedFileHandler, KindFiles)
			if err != nil {
				return nil, err
			}
		}

		if srvConf.API != nil {
			// map backends to endpoint
			endpoints := make(map[string]bool)
			for _, endpoint := range srvConf.API.Endpoint {
				pattern := utils.JoinPath("/", serverOptions.APIBasePath, endpoint.Pattern)

				unique, cleanPattern := isUnique(endpoints, pattern)
				if !unique {
					return nil, fmt.Errorf("duplicate endpoint: %q", pattern)
				}
				endpoints[cleanPattern] = true

				if err := validateInlineScheme(confCtx, endpoint.InlineDefinition, endpoint); err != nil {
					return nil, err
				}

				// setACHandlerFn individual wrap for access_control configuration per endpoint
				setACHandlerFn := func(protectedHandler http.Handler) {
					api[endpoint] = configureProtectedHandler(accessControls, serverOptions.APIErrTpl,
						config.NewAccessControl(srvConf.AccessControl, srvConf.DisableAccessControl).
							Merge(config.NewAccessControl(srvConf.API.AccessControl, srvConf.API.DisableAccessControl)),
						config.NewAccessControl(endpoint.AccessControl, endpoint.DisableAccessControl),
						protectedHandler)
				}

				// lookup for backend reference, prefer endpoint definition over api one
				if endpoint.Backend != "" {
					if _, ok := backends[endpoint.Backend]; !ok {
						return nil, fmt.Errorf("backend %q is not defined", endpoint.Backend)
					}

					// set server context for defined backends
					be := backends[endpoint.Backend]
					_, remain := be.conf.Merge(&config.Backend{Options: endpoint.InlineDefinition})
					refBackend := newProxy(confCtx, be.conf, srvConf.API.CORS, remain, log, serverOptions)

					setACHandlerFn(refBackend)
					err = setRoutesFromHosts(serverConfiguration, defaultPort, srvConf.Hosts, pattern, api[endpoint], KindAPI)
					if err != nil {
						return nil, err
					}
					continue
				}

				// otherwise try to parse an inline block and fallback for api reference or inline block
				inlineBackend, inlineConf, err := newInlineBackend(confCtx, backends, endpoint.InlineDefinition, srvConf.API.CORS, log, serverOptions)
				if err == errorMissingBackend {
					if srvConf.API.Backend != "" {
						if _, ok := backends[srvConf.API.Backend]; !ok {
							return nil, fmt.Errorf("backend %q is not defined", srvConf.API.Backend)
						}
						setACHandlerFn(backends[srvConf.API.Backend].handler)
						err = setRoutesFromHosts(serverConfiguration, defaultPort, srvConf.Hosts, pattern, api[endpoint], KindAPI)
						if err != nil {
							return nil, err
						}
						continue
					}
					inlineBackend, inlineConf, err = newInlineBackend(confCtx, backends, srvConf.API.InlineDefinition, srvConf.API.CORS, log, serverOptions)
					if err != nil {
						return nil, err
					}

					if inlineConf.Name == "" && getAttribute(confCtx, "origin", inlineConf.Options, conf.Bytes) == "" {
						return nil, fmt.Errorf("api inline backend requires an origin attribute: %q", pattern)
					}
				} else if err != nil { // TODO hcl.diagnostics error
					return nil, fmt.Errorf("range: %s: %v", endpoint.InlineDefinition.MissingItemRange().String(), err)
				}

				if e := validateOrigin(
					getAttribute(confCtx, "origin", inlineConf.Options, conf.Bytes),
					inlineConf.Options.MissingItemRange()); e != nil {
					return nil, e
				}

				setACHandlerFn(inlineBackend)
				err = setRoutesFromHosts(serverConfiguration, defaultPort, srvConf.Hosts, pattern, api[endpoint], KindAPI)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	return serverConfiguration, nil
}

func newProxy(ctx *hcl.EvalContext, beConf *config.Backend, corsOpts *config.CORS, remainCtx []hcl.Body, log *logrus.Entry, srvOpts *server.Options) http.Handler {
	corsOptions, err := handler.NewCORSOptions(corsOpts)
	if err != nil {
		log.Fatal(err)
	}

	proxyOptions, err := handler.NewProxyOptions(beConf, corsOptions, remainCtx)
	if err != nil {
		log.Fatal(err)
	}

	proxy, err := handler.NewProxy(proxyOptions, log, srvOpts, ctx)
	if err != nil {
		log.Fatal(err)
	}
	return proxy
}

func newBackendsFromDefinitions(conf *config.Gateway, confCtx *hcl.EvalContext, log *logrus.Entry) (map[string]backendDefinition, error) {
	backends := make(map[string]backendDefinition)

	if conf.Definitions == nil {
		return backends, nil
	}

	for _, beConf := range conf.Definitions.Backend {
		if _, ok := backends[beConf.Name]; ok {
			return nil, fmt.Errorf("backend name must be unique: %q", beConf.Name)
		}

		origin := getAttribute(confCtx, "origin", beConf.Options, conf.Bytes)
		if e := validateOrigin(origin, beConf.Options.MissingItemRange()); e != nil {
			return nil, e
		}

		beConf, _ = defaultBackendConf.Merge(beConf)

		srvOpts, _ := server.NewServerOptions(&config.Server{})
		backends[beConf.Name] = backendDefinition{
			conf:    beConf,
			handler: newProxy(confCtx, beConf, nil, []hcl.Body{beConf.Options}, log, srvOpts),
		}
	}
	return backends, nil
}

// hasAttribute checks for a configured string value and ignores unrelated errors.
func getAttribute(ctx *hcl.EvalContext, name string, body hcl.Body, configBytes []byte) string {
	attr, _ := body.JustAttributes()

	if _, ok := attr[name]; !ok {
		return ""
	}

	val, diags := attr[name].Expr.Value(ctx)
	if diags.HasErrors() && attr[name].Expr.Range().CanSliceBytes(configBytes) { // fallback to origin string
		rawString := attr[name].Expr.Range().SliceBytes(configBytes)
		if len(rawString) > 2 { // more then quotes
			return string(attr[name].Expr.Range().SliceBytes(configBytes)[1 : len(rawString)-1]) //unquote
		}
	}
	return seetie.ValueToString(val)
}

func splitWildcardHostPort(host string, configuredPort int) (string, Port, error) {
	if !strings.Contains(host, ":") {
		return host, Port(configuredPort), nil
	}

	ho := host
	po := configuredPort
	h, p, err := net.SplitHostPort(host)
	if err != nil {
		return "", -1, err
	}
	ho = h
	if p != "" && p != "*" {
		if !rePortCheck.MatchString(p) {
			return "", -1, fmt.Errorf("invalid port given: %s", p)
		}
		po, err = strconv.Atoi(p)
		if err != nil {
			return "", -1, err
		}
	}

	return ho, Port(po), nil
}

func configureAccessControls(conf *config.Gateway, confCtx *hcl.EvalContext) (ac.Map, error) {
	accessControls := make(ac.Map)

	if conf.Definitions != nil {
		for _, ba := range conf.Definitions.BasicAuth {
			name, err := validateACName(accessControls, ba.Name, "basic_auth")
			if err != nil {
				return nil, err
			}

			basicAuth, err := ac.NewBasicAuth(name, ba.User, ba.Pass, ba.File, ba.Realm)
			if err != nil {
				return nil, err
			}

			accessControls[name] = basicAuth
		}

		for _, jwt := range conf.Definitions.JWT {
			name, err := validateACName(accessControls, jwt.Name, "jwt")
			if err != nil {
				return nil, err
			}

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
				wd, err := os.Getwd()
				if err != nil {
					return nil, err
				}
				content, err := ioutil.ReadFile(path.Join(wd, jwt.KeyFile))
				if err != nil {
					return nil, err
				}
				key = content
			} else if jwt.Key != "" {
				key = []byte(jwt.Key)
			}

			var claims ac.Claims
			if jwt.Claims != nil {
				c, diags := seetie.ExpToMap(confCtx, jwt.Claims)
				if diags.HasErrors() {
					return nil, diags
				}
				claims = c
			}
			j, err := ac.NewJWT(jwt.SignatureAlgorithm, name, claims, jwt.ClaimsRequired, jwtSource, jwtKey, key)
			if err != nil {
				return nil, fmt.Errorf("loading jwt %q definition failed: %s", name, err)
			}

			accessControls[name] = j
		}
	}

	return accessControls, nil
}

func configureProtectedHandler(m ac.Map, errTpl *errors.Template, parentAC, handlerAC config.AccessControl, h http.Handler) http.Handler {
	var acList ac.List
	for _, acName := range parentAC.
		Merge(handlerAC).List() {
		m.MustExist(acName)
		acList = append(acList, m[acName])
	}
	if len(acList) > 0 {
		return handler.NewAccessControl(h, errTpl, acList...)
	}
	return h
}

func newInlineBackend(evalCtx *hcl.EvalContext, backends map[string]backendDefinition, inlineDef hcl.Body, cors *config.CORS, log *logrus.Entry, srvOpts *server.Options) (http.Handler, *config.Backend, error) {
	content, _, diags := inlineDef.PartialContent(config.Endpoint{}.Schema(true))
	if diags.HasErrors() {
		return nil, nil, diags
	}

	if content == nil || len(content.Blocks) == 0 {
		return nil, nil, errorMissingBackend
	}

	var inlineBlock *hcl.Block
	for _, block := range content.Blocks {
		if block.Type == "backend" {
			inlineBlock = block
		}
	}
	if inlineBlock == nil {
		return nil, nil, errorMissingBackend
	}

	if err := validateInlineScheme(evalCtx, inlineBlock.Body, config.Backend{}); err != nil {
		return nil, nil, err
	}

	beConf := &config.Backend{}
	diags = gohcl.DecodeBody(inlineBlock.Body, evalCtx, beConf)
	if diags.HasErrors() {
		return nil, nil, diags
	}

	beConf, _ = defaultBackendConf.Merge(beConf)
	if len(content.Blocks[0].Labels) > 0 {
		beConf.Name = content.Blocks[0].Labels[0]
		if beRef, ok := backends[beConf.Name]; ok {
			beConf, _ = beRef.conf.Merge(beConf)
		} else {
			return nil, nil, fmt.Errorf("override backend %q is not defined", beConf.Name)
		}
	}

	proxy := newProxy(evalCtx, beConf, cors, []hcl.Body{beConf.Options}, log, srvOpts)
	return proxy, beConf, nil
}

func setRoutesFromHosts(srvConf *ServerConfiguration, confPort int, hosts []string, path string, handler http.Handler, kind HandlerKind) error {
	hostList := hosts
	if len(hostList) == 0 {
		hostList = []string{"*"}
	}

	for _, h := range hostList {
		joinedPath := utils.JoinPath("/", path)
		host, listenPort, err := splitWildcardHostPort(h, confPort)
		if err != nil {
			return err
		}

		if host != "*" {
			joinedPath = utils.JoinPath(
				pathpattern.PathFromHost(
					net.JoinHostPort(host, listenPort.String()), false), "/", path)
		}

		var routes map[string]http.Handler

		switch kind {
		case KindAPI:
			routes = srvConf.PortOptions[listenPort].EndpointRoutes
		case KindFiles:
			routes = srvConf.PortOptions[listenPort].FileRoutes
		case KindSPA:
			routes = srvConf.PortOptions[listenPort].SPARoutes
		default:
			return fmt.Errorf("unknown route kind")
		}

		if _, exist := routes[joinedPath]; exist {
			return fmt.Errorf("duplicate route found on port %q: %q", listenPort.String(), path)
		}
		routes[joinedPath] = handler
	}
	return nil
}
