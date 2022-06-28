package corednscontainer

import (
	"context"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/coredns/coredns/request"
	dockerapi "github.com/fsouza/go-dockerclient"
	"github.com/miekg/dns"
)

type workItem struct {
	ctx    context.Context
	base   string
	full   string
	qtype  uint16
	answer chan<- answer
}
type answer struct {
	network string
	isV4    bool
	dns.RR
}

func newWorkItem(ctx context.Context, state *request.Request, domains []string, answer chan<- answer) workItem {
	w := workItem{
		ctx:    ctx,
		full:   state.Name(),
		qtype:  state.QType(),
		answer: answer,
	}

	if len(domains) == 0 {
		w.base, _, _ = strings.Cut(w.full, ".")
	} else {
		w.base = strings.TrimRight(w.full, ".")
	}

	log.Debugf("w: %#v", w)

	return w
}

type worker struct {
	stop     context.Context
	socket   string
	client   *dockerapi.Client
	backoff  time.Duration
	requests chan workItem

	containers []dockerapi.APIContainers

	config
}

func (c *Container) start() {
	log.Infof("start (%d sockets, %d domains)", len(c.sockets), len(c.domains))
	for _, socket := range c.sockets {
		w := &worker{
			c.stop,
			socket,
			nil,
			time.Second,
			make(chan workItem),

			nil,

			c.config,
		}
		go w.connectLoop()
		c.workers = append(c.workers, w.requests)
	}
}

func (w *worker) connectLoop() {
	for w.stop.Err() == nil {
		log.Infof("connecting to '%s'", w.socket)
		client, err := dockerapi.NewClient(w.socket)
		if err != nil {
			log.Warningf("Cannot connect to socket '%s', retry in %v", w.socket, w.backoff)
			time.Sleep(w.backoff)
			// will max out at 2*10 seconds -> ~17 minutes
			if w.backoff < 15*time.Minute {
				w.backoff *= 2
			}
			continue
		}

		w.client = client

		// reset backoff
		w.backoff = time.Second

		// will return on error, then we'll reconnect
		w.clientWorker()
	}
}

func (w *worker) clientWorker() {
	refresh := time.NewTicker(w.config.cacheTime)
	defer refresh.Stop()

	events := make(chan *dockerapi.APIEvents)
	defer close(events)

	w.client.AddEventListener(events)

	for {
		select {
		case <-refresh.C:
			w.containers = nil
		case r := <-w.requests:
			log.Debugf("worker: request: %v", r.full)
			if w.containers == nil && !w.refresh() {
				close(r.answer)
				return
			}
			w.handleRequest(r, w.containers)
		case e := <-events:
			log.Debugf("event: %s -> %s (%s)", e.Action, e.Actor.Attributes["name"], e.ID[0:12])
			w.containers = nil
		case <-w.stop.Done():
			return
		}
	}
}

func (w *worker) refresh() bool {
	log.Debugf("worker: refresh '%s'", w.socket)
	containers, err := w.client.ListContainers(dockerapi.ListContainersOptions{})
	if err == nil {
		w.containers = containers
		return true
	}

	log.Debugf("worker: refresh failed for '%s': %s", w.socket, err)
	return false
}

func (w *worker) handleRequest(r workItem, containers []dockerapi.APIContainers) {
	defer log.Debugf("worker: end request: %v", r.full)
	defer close(r.answer)

	wg := &sync.WaitGroup{}
	wg.Add(len(containers))

	for _, c := range containers {
		go func(c dockerapi.APIContainers) {
			defer wg.Done()

			for _, name := range c.Names {
				name := strings.TrimRight(name[1:], ".")

				log.Debugf("worker: inspecting %v", name)

				log.Debugf("name: %s", name)
				if r.base == name {
					log.Debugf("worker: found %s", name)
					pushIPs(r.full, r.qtype, c, r.answer)
					return
				}
			}
		}(c)
	}

	wg.Wait()
}

func pushIPs(name string, qtype uint16, c dockerapi.APIContainers, output chan<- answer) {
	for netName, network := range c.Networks.Networks {
		log.Debugf("worker: %s.%s: v4=%v v6=%v", c.Names[0][1:], netName, network.IPAddress, network.GlobalIPv6Address)

		if qtype == dns.TypeA {
			pushA(name, netName, c, output, net.ParseIP(network.IPAddress))
		} else {
			pushAAAA(name, netName, c, output, net.ParseIP(network.GlobalIPv6Address))
		}
	}
}

func pushA(name, netName string, c dockerapi.APIContainers, output chan<- answer, ip net.IP) {
	if ip == nil {
		return
	}

	rr := &dns.A{
		A: ip,
	}

	pushRR(name, netName, rr, dns.TypeA, &rr.Hdr, output)
}

func pushAAAA(name, netName string, c dockerapi.APIContainers, output chan<- answer, ip net.IP) {
	if ip == nil {
		return
	}

	rr := &dns.AAAA{
		AAAA: ip,
	}

	pushRR(name, netName, rr, dns.TypeAAAA, &rr.Hdr, output)
}

func pushRR(name, netName string, rr dns.RR, t uint16, hdr *dns.RR_Header, output chan<- answer) {
	*hdr = dns.RR_Header{
		Name:   name,
		Rrtype: t,
		Class:  dns.ClassINET,
		Ttl:    30,
	}

	output <- answer{
		netName,
		t == dns.TypeA,
		rr,
	}
}
