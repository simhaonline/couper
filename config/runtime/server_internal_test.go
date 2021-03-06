package runtime

import (
	"fmt"
	"testing"

	"github.com/avenga/couper/config"
)

func TestServer_isUnique(t *testing.T) {
	endpoints := make(map[string]bool)

	type testCase struct {
		pattern string
		clean   string
		unique  bool
	}

	for i, tc := range []testCase{
		{"/abc", "/abc", true},
		{"/abc", "/abc", false},
		{"/abc/", "/abc/", true},
		{"/x/{xxx}", "/x/{}", true},
		{"/x/{yyy}", "/x/{}", false},
		{"/x/{xxx}/a/{yyy}", "/x/{}/a/{}", true},
		{"/x/{yyy}/a/{xxx}", "/x/{}/a/{}", false},
	} {
		unique, cleanPattern := isUnique(endpoints, tc.pattern)
		endpoints[cleanPattern] = true

		if unique != tc.unique {
			t.Errorf("%d: Unexpected unique status given: %t", i+1, unique)
		}
		if cleanPattern != tc.clean {
			t.Errorf("%d: Unexpected cleanPattern given: %s, want %s", i+1, cleanPattern, tc.clean)
		}
	}
}

func TestServer_validatePortHosts(t *testing.T) {
	type args struct {
		conf           *config.Gateway
		configuredPort int
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			"Same host/port in one server",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"*", "*", "*:9090", "*:9090", "*:8080"}},
					},
				}, 8080,
			},
			false,
		},
		{
			"Same host/port in two servers with *",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"*"}},
						{Hosts: []string{"*"}},
					},
				}, 8080,
			},
			true,
		},
		{
			"Same host/port in two servers with *:<port>",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"*:8080"}},
						{Hosts: []string{"*:8080"}},
					},
				}, 8080,
			},
			true,
		},
		{
			"Same host/port in two servers with example.com",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"example.com", "couper.io"}},
						{Hosts: []string{"example.com", "couper.io"}},
					},
				}, 8080,
			},
			true,
		},
		{
			"Same host/port in two servers with example.com:<port>",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"example.com:9090"}},
						{Hosts: []string{"example.com:9090"}},
					},
				}, 8080,
			},
			true,
		},
		{
			"Same port w/ different host in two servers",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"*", "example.com:9090"}},
						{Hosts: []string{"couper.io:9090"}},
					},
				}, 8080,
			},
			false,
		},
		{
			"Host is mandatory for multiple servers",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{Hosts: []string{"*"}},
						{},
					},
				}, 8080,
			},
			true,
		},
		{
			"Host is optional for single server",
			args{
				&config.Gateway{
					Server: []*config.Server{
						{},
					},
				}, 8080,
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := validatePortHosts(tt.args.conf, tt.args.configuredPort); (err != nil) != tt.wantErr {
				t.Errorf("validatePortHosts() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestServer_splitWildcardHostPort(t *testing.T) {
	for _, port := range []string{"01234", "00", "123456"} {
		host := fmt.Sprintf("foo.de:%s", port)
		_, _, err := splitWildcardHostPort(host, 8080)
		if err == nil {
			t.Errorf("Expected en error for %q, NIL given.", host)
		}
	}

	_, _, err := splitWildcardHostPort("1", 8080)
	if err != nil {
		t.Errorf("Expected NIL, given %s", err)
	}
}
