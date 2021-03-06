package config

import "github.com/hashicorp/hcl/v2"

type Gateway struct {
	Bytes       []byte           `hcl:"-"`
	Context     *hcl.EvalContext `hcl:"-"`
	Definitions *Definitions     `hcl:"definitions,block"`
	Server      []*Server        `hcl:"server,block"`
	Settings    *Settings        `hcl:"settings,block"`
}
