package corednscontainer

import (
	"context"
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
	log.Debug("query")
	state := request.Request{Req: r, W: w}

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
		w <- workItem{subCtx, &state, workerAnswers}
	}

	m := new(dns.Msg)
	m.SetReply(r)
	m.Authoritative, m.RecursionAvailable, m.Compress = true, true, true
	m.Answer = []dns.RR{}

	var found bool

	log.Debug("<- answers")

	for rr := range answers {
		found = true
		m.Answer = append(m.Answer, rr.RR)
	}

	log.Debug("-> response")

	if found {
		log.Debugf("sending %d records", len(m.Answer))
	} else {
		log.Debug("not found")
	}
	w.WriteMsg(m)
	return dns.RcodeSuccess, nil
}

func (*Container) Name() string {
	return "container"
}
