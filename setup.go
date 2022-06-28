package corednscontainer

import (
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

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return New(next, sockets)
	})

	return nil
}
