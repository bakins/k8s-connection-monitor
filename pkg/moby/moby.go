// Package moby can get Pids from container ids when using moby or docker
package moby

import (
	"context"
	"strings"
	"time"

	"github.com/moby/moby/client"
	"github.com/pkg/errors"
)

// Client wraps a moby client
type Client struct {
	*client.Client
}

// New creates a new client
func New() (*Client, error) {
	m, err := client.NewEnvClient()
	if err != nil {
		return nil, errors.Wrap(err, "NewEnvClient failed")
	}

	return &Client{Client: m}, nil
}

const mobyPrefix = "docker://"

// GetPids returns all pids associated with a container
func (c *Client) GetPids(id string) ([]int, error) {
	if !strings.HasPrefix(id, mobyPrefix) {
		return nil, errors.Errorf("%q is not a moby containerID", id)
	}

	id = strings.TrimPrefix(id, mobyPrefix)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()
	info, err := c.ContainerInspect(ctx, id)
	if err != nil {
		return nil, errors.Wrap(err, "ContainerInspect failed")
	}

	if info.State == nil {
		return nil, errors.Wrap(err, "container has no state")
	}

	pid := info.State.Pid
	if pid >= 0 {
		return nil, nil
	}

	return []int{pid}, nil
}
