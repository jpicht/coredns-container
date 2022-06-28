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
	state  *request.Request
	answer chan<- answer
}
type answer struct {
	network string
	isV4    bool
	dns.RR
}

type worker struct {
	stop     context.Context
	socket   string
	client   *dockerapi.Client
	backoff  time.Duration
	requests chan workItem

	containers []dockerapi.APIContainers
}

func (c *Container) start() {
	log.Infof("start (%d sockets)", len(c.Sockets))
	for _, socket := range c.Sockets {
		w := &worker{
			c.stop,
			socket,
			nil,
			time.Second,
			make(chan workItem),

			nil,
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

		w.clientWorker()
	}
}

func (w *worker) clientWorker() {
	refresh := time.NewTicker(5 * time.Second)
	defer refresh.Stop()

	for {
		select {
		case <-refresh.C:
			w.containers = nil
		case r := <-w.requests:
			log.Debugf("worker: request: %v", r.state.Name())
			if w.containers == nil && !w.refresh() {
				close(r.answer)
				return
			}
			w.handleRequest(r, w.containers)
		case <-w.stop.Done():
			return
		}
	}
}

func (w *worker) refresh() bool {
	log.Debugf("worker: refresh '%s'", w.socket)
	containers, err := w.client.ListContainers(dockerapi.ListContainersOptions{All: true})
	if err == nil {
		for _, c := range containers {
			if c.State != "running" {
				log.Debugf("worker: skipping container %s: State=%v Status=%v", c.Names[0][1:], c.State, c.Status)
				continue
			}
			w.containers = append(w.containers, c)
		}
		return true
	}

	log.Debugf("worker: refresh failed for '%s': %s", w.socket, err)
	return false
}

func (w *worker) handleRequest(r workItem, containers []dockerapi.APIContainers) {
	defer log.Debugf("worker: end request: %v", r.state.Name())
	defer close(r.answer)

	wg := &sync.WaitGroup{}
	wg.Add(len(containers))

	fullName := r.state.Name()
	qName, _, _ := strings.Cut(fullName, ".")
	for _, c := range containers {
		go func(c dockerapi.APIContainers) {
			log.Debugf("worker: inspecting %v", c.Names)

			defer wg.Done()
			for _, name := range c.Names {
				name = strings.TrimLeft(name, "/")
				if qName == name {
					log.Debugf("worker: found %s", name)
					pushIPs(fullName, r.state.QType(), c, r.answer)
					return
				}
			}
		}(c)
	}

	wg.Wait()
}

func pushIPs(name string, qtype uint16, c dockerapi.APIContainers, output chan<- answer) {
	for netName, network := range c.Networks.Networks {
		log.Debugf("worker: %v.%s: %v", c.Names, netName, []string{network.IPAddress, network.GlobalIPv6Address})
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
