package client

import (
	"fmt"
	"sync"

	log "github.com/Sirupsen/logrus"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/client"
	"golang.org/x/net/context"
)

const (
	//should really find a better place for this
	//rather than duplicating the driver name
	networkDriverName = "vxrNet"
)

// Client is a wrapper for docker client type things
type Client struct {
	dc          *client.Client
	nrByID      map[string]*types.NetworkResource
	nrByPool    map[string]*types.NetworkResource
	nrCacheLock *sync.RWMutex
}

// NewClient creates a new client
func NewClient() (*Client, error) {
	dc, err := client.NewEnvClient()
	if err != nil {
		return nil, err
	}

	c := &Client{
		dc:          dc,
		nrByID:      make(map[string]*types.NetworkResource),
		nrByPool:    make(map[string]*types.NetworkResource),
		nrCacheLock: &sync.RWMutex{},
	}

	return c, nil
}

// GetContainers gets a list of docker containers
func (c *Client) GetContainers() ([]types.Container, error) {
	return c.dc.ContainerList(context.Background(), types.ContainerListOptions{})
}

// GetNetworkResourceByID gets a network resource by ID (checks cache first)
func (c *Client) GetNetworkResourceByID(id string) (*types.NetworkResource, error) {
	log := log.WithField("net_id", id)
	log.Debug("getNetworkResourceByID")

	//first check local cache with a read-only mutex
	c.nrCacheLock.RLock()

	if nr, ok := c.nrByID[id]; ok {
		c.nrCacheLock.RUnlock()
		return nr, nil
	}
	c.nrCacheLock.RUnlock()

	//netid wasn't in cache, fetch from docker inspect
	nr, err := c.dc.NetworkInspect(context.Background(), id)
	if err != nil {
		log.WithError(err).Error("failed to inspect network")
		return nil, err
	}

	//add nr pointer to both caches
	c.cacheNetworkResource(&nr)

	return &nr, nil
}

// GetNetworkResourceByPool gets a network resource by it's subnet
func (c *Client) GetNetworkResourceByPool(pool string) (*types.NetworkResource, error) {
	log := log.WithField("pool", pool)
	log.Debug("getNetworkResourceByPool")

	//not sure of the performance implications of sharing a read lock between
	//both caches, but we want them in lock step anyway, so likely a non-issue
	c.nrCacheLock.RLock()

	if nr, ok := c.nrByPool[pool]; ok {
		c.nrCacheLock.RUnlock()
		return nr, nil
	}
	c.nrCacheLock.RUnlock()

	flts := filters.NewArgs()
	flts.Add("driver", networkDriverName)
	nl, err := c.dc.NetworkList(context.Background(), types.NetworkListOptions{Filters: flts})
	if err != nil {
		log.WithError(err).Error("failed to list networks")
		return nil, err
	}

	var nr *types.NetworkResource
	for _, n := range nl {
		tnr, err := c.GetNetworkResourceByID(n.ID)
		if err != nil {
			continue
		}
		tp, _ := poolFromNR(tnr) // nolint errcheck
		if tp == pool {
			nr = tnr
			break
		}
	}

	return nr, nil
}

func (c *Client) cacheNetworkResource(nr *types.NetworkResource) {
	c.nrCacheLock.Lock()
	defer c.nrCacheLock.Unlock()

	pool, err := poolFromNR(nr)
	if err != nil {
		log.Debug("failed to get pool from network resource, not caching")
		return
	}

	c.nrByID[nr.ID] = nr
	c.nrByPool[pool] = nr
}

func poolFromNR(nr *types.NetworkResource) (string, error) {
	for _, c := range nr.IPAM.Config {
		if c.Subnet != "" {
			return c.Subnet, nil
		}
	}
	return "", fmt.Errorf("pool not found")
}
