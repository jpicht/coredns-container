package corednscontainer

import (
	"strings"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
)

func init() { plugin.Register("container", setup) }

func setup(c *caddy.Controller) error {
	c.Next() // skip name

	var (
		config = config{
			cacheTime: 5 * time.Second,
		}
	)

	config.sockets = c.RemainingArgs()
	if len(config.sockets) < 1 {
		return c.ArgErr()
	}

	if c.NextBlock() {
		key := c.Val()
		switch key {
		case "domain":
			for c.NextArg() {
				domain := c.Val()
				if !strings.HasSuffix(domain, ".") {
					domain += "."
				}
				config.domains = append(config.domains, domain)
			}
		case "cache":
			if !c.NextArg() {
				return c.ArgErr()
			}
			cacheTime_, err := time.ParseDuration(c.Val())
			if err != nil {
				return c.Errf("'%s' is not a valid duration: %w", c.Val, err)
			}
			config.cacheTime = cacheTime_
		}
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return New(next, config)
	})

	return nil
}
