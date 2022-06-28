package corednscontainer

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"github.com/coredns/coredns/request"
	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin("container")

type Container struct {
	stop   context.Context
	cancel context.CancelFunc

	Next    plugin.Handler
	sockets []string
	domains []string

	workers []chan<- workItem
}

func New(next plugin.Handler, sockets []string, domains []string) *Container {
	stop, cancel := context.WithCancel(context.Background())
	c := &Container{
		stop:    stop,
		cancel:  cancel,
		Next:    next,
		sockets: sockets,
		domains: domains,
	}
	c.start()
	return c
}

func (c *Container) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	log.Debug("query")
	state := request.Request{Req: r, W: w}
	var ok = len(c.domains) == 0
	if len(c.domains) > 0 {
		name := state.Name()
		for _, domain := range c.domains {
			if strings.HasSuffix(name, domain) {
				ok = true
			}
		}
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative, m.RecursionAvailable, m.Compress = true, true, true
	if ok {
		m.Answer = c.get(ctx, state)
	}

	log.Debug("-> response")
	log.Debugf("sending %d records", len(m.Answer))

	w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (c *Container) get(ctx context.Context, state request.Request) []dns.RR {
	var (
		subCtx context.Context
		cancel context.CancelFunc
	)

	if deadline, ok := ctx.Deadline(); ok {
		subCtx, cancel = context.WithTimeout(ctx, time.Until(deadline)*time.Duration(9)/time.Duration(10))
	} else {
		subCtx, cancel = context.WithTimeout(ctx, time.Second)
	}
	defer cancel()

	log.Debug("-> worker")

	answers := make(chan answer)

	wg := &sync.WaitGroup{}
	wg.Add(len(c.workers))
	go func() {
		wg.Wait()
		close(answers)
	}()

	for _, w := range c.workers {
		workerAnswers := make(chan answer)
		go func() {
			defer wg.Done()
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
		w <- newWorkItem(subCtx, &state, c.domains, workerAnswers)
	}

	log.Debug("<- answers")

	resultSet := []dns.RR{}
	for rr := range answers {
		resultSet = append(resultSet, rr)
	}

	return resultSet
}

func (*Container) Name() string {
	return "container"
}
