package corednscontainer

import (
	"strings"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

func init() { plugin.Register("container", setup) }

func setup(c *caddy.Controller) error {
	c.Next() // skip name

	sockets := c.RemainingArgs()
	if len(sockets) < 1 {
		return c.ArgErr()
	}

	domains := []string{}
	if c.NextBlock() {
		key := c.Val()
		switch key {
		case "domain":
			for c.NextArg() {
				domain := c.Val()
				if !strings.HasSuffix(domain, ".") {
					domain += "."
				}
				domains = append(domains, domain)
			}
		}
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return New(next, sockets, domains)
	})

	return nil
}
