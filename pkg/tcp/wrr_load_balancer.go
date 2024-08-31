package tcp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog/log"
)

type server struct {
	Handler
	weight int
}

// WRRLoadBalancer is a naive RoundRobin load balancer for TCP services.
type WRRLoadBalancer struct {
	servers       []server
	lock          sync.Mutex
	currentWeight int
	index         int
	// status is a record of which child services of the Balancer are healthy.
	status map[string]*atomic.Bool
	// updaters is the list of hooks that are run (to update the Balancer
	// parent(s)), whenever the Balancer status changes.
	updaters []func(bool)
}

// NewWRRLoadBalancer creates a new WRRLoadBalancer.
func NewWRRLoadBalancer() *WRRLoadBalancer {
	return &WRRLoadBalancer{
		index: -1,
	}
}

// ServeTCP forwards the connection to the right service.
func (b *WRRLoadBalancer) ServeTCP(conn WriteCloser) {
	b.lock.Lock()
	next, err := b.next()
	b.lock.Unlock()

	if err != nil {
		log.Error().Err(err).Msg("Error during load balancing")
		conn.Close()
		return
	}

	next.ServeTCP(conn)
}

// AddServer appends a server to the existing list.
func (b *WRRLoadBalancer) AddServer(serverHandler Handler) {
	w := 1
	b.AddWeightServer(serverHandler, &w)
}

// AddWeightServer appends a server to the existing list with a weight.
func (b *WRRLoadBalancer) AddWeightServer(serverHandler Handler, weight *int) {
	b.lock.Lock()
	defer b.lock.Unlock()

	w := 1
	if weight != nil {
		w = *weight
	}
	b.servers = append(b.servers, server{Handler: serverHandler, weight: w})
}

func (b *WRRLoadBalancer) maxWeight() int {
	max := -1
	for _, s := range b.servers {
		if s.weight > max {
			max = s.weight
		}
	}
	return max
}

func (b *WRRLoadBalancer) weightGcd() int {
	divisor := -1
	for _, s := range b.servers {
		if divisor == -1 {
			divisor = s.weight
		} else {
			divisor = gcd(divisor, s.weight)
		}
	}
	return divisor
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

func (b *WRRLoadBalancer) next() (Handler, error) {
	if len(b.servers) == 0 {
		return nil, errors.New("no servers in the pool")
	}

	// The algo below may look messy, but is actually very simple
	// it calculates the GCD  and subtracts it on every iteration, what interleaves servers
	// and allows us not to build an iterator every time we readjust weights

	// Maximum weight across all enabled servers
	max := b.maxWeight()
	if max == 0 {
		return nil, errors.New("all servers have 0 weight")
	}

	// GCD across all enabled servers
	gcd := b.weightGcd()

	for {
		b.index = (b.index + 1) % len(b.servers)
		if b.index == 0 {
			b.currentWeight -= gcd
			if b.currentWeight <= 0 {
				b.currentWeight = max
			}
		}
		srv := b.servers[b.index]
		if srv.weight >= b.currentWeight {
			return srv, nil
		}
	}
}

// SetStatus sets status (UP or DOWN) of a target server.
func (b *WRRLoadBalancer) SetStatus(ctx context.Context, childName string, up bool) {
	statusString := "DOWN"
	if up {
		statusString = "UP"
	}

	log.Ctx(ctx).Debug().Msgf("Setting status of %s to %s", childName, statusString)

	currentStatus, exists := b.status[childName]
	if !exists {
		s := &atomic.Bool{}
		s.Store(up)
		b.status[childName] = s
		return
	}

	if !currentStatus.CompareAndSwap(!up, up) {
		log.Ctx(ctx).Debug().Msgf("Still %s, no need to propagate", statusString)
		return
	}

	log.Ctx(ctx).Debug().Msgf("Propagating new %s status", statusString)
	for _, fn := range b.updaters {
		fn(up)
	}
}
