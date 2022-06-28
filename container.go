package corednscontainer

import (
	"context"
	"time"

	"github.com/coredns/coredns/plugin"
	"github.com/miekg/dns"
)

type Container struct {
	stop   context.Context
	cancel context.CancelFunc

	Next    plugin.Handler
	Sockets []string

	workers []chan<- workItem
}

func New(next plugin.Handler, sockets []string) *Container {
	stop, cancel := context.WithCancel(context.Background())
	c := &Container{
		stop:    stop,
		cancel:  cancel,
		Next:    next,
		Sockets: sockets,
	}
	c.start()
	return c
}

func (c *Container) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {

	answers := make(chan answer)

	var (
		subCtx context.Context
		cancel context.CancelFunc
	)

	if deadline, ok := ctx.Deadline(); ok {
		subCtx, cancel = context.WithTimeout(ctx, time.Until(deadline)*time.Duration(9)/time.Duration(10))
	} else {
		subCtx, cancel = context.WithCancel(ctx)
	}
	defer cancel()

	for _, w := range c.workers {
		workerAnswers := make(chan answer)
		go func() {
			for {
				select {
				case answer, ok := <-workerAnswers:
					if !ok {
						return
					}
					answers <- answer
				case <-subCtx.Done():
					return
				}
			}
		}()
		w <- workItem{subCtx, r, workerAnswers}
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative, m.RecursionAvailable, m.Compress = true, true, true
	m.Answer = []dns.RR{}

	var found bool

	for rr := range answers {
		found = true
		m.Answer = append(m.Answer, rr)
	}

	if found {
		w.WriteMsg(m)
		return dns.RcodeSuccess, nil
	}

	return c.Next.ServeDNS(ctx, w, r)
}

func (*Container) Name() string {
	return "container"
}
