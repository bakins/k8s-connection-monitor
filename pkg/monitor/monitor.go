// Package monitor provides the high level functionality for monitoring open
// connections on a Kubernetes node
package monitor

import (
	"net"
	"os"
	"strings"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"k8s.io/api/core/v1"
)

// PodLister implements functions for getting list of pods on a node
type PodLister interface {
	ListPods(node string) ([]v1.Pod, error)
}

// PidGetter gets pids of containers given a container ID.
// This should be the pid in the host namespace as monitor
// should run there.
type PidGetter interface {
	GetPids(id string) ([]int, error)
}

// ConnectionGetter gets connections info given a pid.
// Note: pid is used for network namespaces, not necessarily the connections
// only associated with a given pid.
type ConnectionGetter interface {
	// both connections and errors can be returned
	GetConnections(pid int) ([]Connection, error)
}

// Connection represents a connection - could also be a listening socket
type Connection struct {
	Family       string
	Type         string
	LocalAddress string
	RemoteAddess string
	Status       string
}

// TODO: track ip and port separately?

// Monitor wraps getting pods and their connections
type Monitor struct {
	podLister        PodLister
	pidGetter        PidGetter
	connectionGetter ConnectionGetter
	logger           *zap.Logger
	nodeName         string
}

// Option is used for setting options on a new Monitor
type Option func(*Monitor) error

// New creates a new Monitor
func New(pl PodLister, pg PidGetter, cg ConnectionGetter, options ...Option) (*Monitor, error) {
	m := &Monitor{
		podLister:        pl,
		pidGetter:        pg,
		connectionGetter: cg,
	}

	for _, f := range options {
		if err := f(m); err != nil {
			return nil, errors.Wrap(err, "options failed")
		}
	}

	if m.logger == nil {
		l, err := zap.NewProduction()
		if err != nil {
			return nil, errors.Wrap(err, "failed to create default logger")
		}
		m.logger = l
	}

	if m.nodeName == "" {
		hostname, err := getFQDN()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get FQDN hostname")
		}
		m.nodeName = hostname
	}
	// so all our logs will include nodename
	m.logger = m.logger.With(zap.String("nodeName", m.nodeName))
	return m, nil
}

// WithLogger returns an Option with sets the logger to use for the Monitor.
// Note: the monitor creates a sub logger for itself, so changes to the logger
// will not be reflected in the monitor.
func WithLogger(logger *zap.Logger) Option {
	return func(m *Monitor) error {
		m.logger = logger
		return nil
	}
}

// WithNodeName returns an Option with sets the node name for the Monitor.
// Default is fqdn hostname
func WithNodeName(nodeName string) Option {
	return func(m *Monitor) error {
		m.nodeName = nodeName
		return nil
	}
}

// Pod is a simplified pod for reporting.
type Pod struct {
	Name      string
	Namespace string
}

// Collect gets running pods on the node, gets pids associated with those pods
// and returns connections associated with them.
// the
func (m *Monitor) Collect() (map[Pod][]Connection, error) {
	pods, err := m.podLister.ListPods(m.nodeName)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to get pods for node %q", m.nodeName)
	}

	out := make(map[Pod][]Connection)

	pods = runningPods(pods)

	for _, p := range pods {
		// get container id's for all running containers
		ids := runningContainerIDs(p.Status)

		pids := make(map[int]bool)
		// get pids for each container. Note: we just log errors
		// as the container could have exited, etc
		for name, id := range ids {
			containerPids, err := m.pidGetter.GetPids(id)
			if err != nil {
				m.logger.Warn(
					"failed to get pids",
					zap.String("name", p.ObjectMeta.Name),
					zap.String("namespace", p.ObjectMeta.Namespace),
					zap.String("container", name),
					zap.String("containerID", id),
				)
				continue
			}
			for _, pid := range containerPids {
				pids[pid] = true
			}
		}

		// get connections associated with each pid
		connections := make(map[string]Connection)
		for pid := range pids {
			// just log error as process may have exited, etc.
			// both connections and errors can be returned!
			c, err := m.connectionGetter.GetConnections(pid)
			if err != nil {
				m.logger.Warn(
					"failed to get pids",
					zap.String("name", p.ObjectMeta.Name),
					zap.String("namespace", p.ObjectMeta.Namespace),
					zap.Int("pid", pid),
				)
			}

			for _, i := range c {
				// we skip status as it may change through a connections lifetime.
				key := strings.Join([]string{i.Family, i.Type, i.LocalAddress, i.RemoteAddess}, "|")
				// last one wins - not it may not be the most recent status, however
				connections[key] = i
			}
		}

		pod := Pod{
			Name:      p.ObjectMeta.Name,
			Namespace: p.ObjectMeta.Namespace,
		}

		conns := make([]Connection, 0, len(connections))
		for _, c := range connections {
			conns = append(conns, c)
		}
		out[pod] = conns
	}

	return out, nil
}

func isContainerRunning(c v1.ContainerStatus) bool {
	return c.State.Running != nil
}

func runningContainerIDs(in v1.PodStatus) map[string]string {
	out := make(map[string]string)
	// TODO: check init containers as well?
	for _, c := range in.ContainerStatuses {
		if isContainerRunning(c) {
			if c.ContainerID != "" {
				out[c.Name] = c.ContainerID
			}
		}
	}
	return out
}

func runningPods(in []v1.Pod) []v1.Pod {
	out := make([]v1.Pod, 0, len(in))

	for _, p := range in {
		p := p
		switch p.Status.Phase {
		case v1.PodRunning, v1.PodPending:
			out = append(out, p)
		}
	}

	return out
}

// based on https://github.com/Showmax/go-fqdn/blob/master/fqdn.go
func getFQDN() (string, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return "", errors.Wrap(err, "failed to get hostname")
	}

	addrs, err := net.LookupIP(hostname)
	if err != nil {
		return hostname, nil
	}

	for _, addr := range addrs {
		if ipv4 := addr.To4(); ipv4 != nil {
			ip, err := ipv4.MarshalText()
			if err != nil {
				return hostname, nil
			}
			hosts, err := net.LookupAddr(string(ip))
			if err != nil || len(hosts) == 0 {
				return hostname, nil
			}
			fqdn := hosts[0]
			return strings.TrimSuffix(fqdn, "."), nil
		}
	}
	return hostname, nil
}
