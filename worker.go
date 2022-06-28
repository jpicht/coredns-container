package corednscontainer

import (
	"context"
	"log"
	"time"

	dockerapi "github.com/fsouza/go-dockerclient"
	"github.com/miekg/dns"
)

type workItem struct {
	ctx    context.Context
	msg    *dns.Msg
	answer chan<- answer
}
type answer dns.RR

type worker struct {
	stop     context.Context
	socket   string
	client   *dockerapi.Client
	backoff  time.Duration
	requests chan workItem
}

func (c *Container) start() {
	for _, socket := range c.Sockets {
		w := &worker{c.stop, socket, nil, time.Second, make(chan workItem)}
		go w.connectLoop()
		c.workers = append(c.workers, w.requests)
	}
}

func (w *worker) connectLoop() {
	for w.stop.Err() == nil {
		client, err := dockerapi.NewClient(w.socket)
		if err != nil {
			log.Printf("Cannot connect to socket '%s', retry in %v", w.socket, w.backoff)
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
	for {
		select {
		case request := <-w.requests:
			w.handleRequest(request)
		case <-w.stop.Done():
			return
		}
	}
}

func (w *worker) handleRequest(r workItem) {
	defer close(r.answer)
}
