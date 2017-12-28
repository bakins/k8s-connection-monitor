// +build linux

package connection

import (
	"bytes"
	"encoding/hex"
	"io/ioutil"
	"net"
	"path/filepath"
	"strconv"
	"strings"

	monitor "github.com/bakins/k8s-connection-monitor"
	multierror "github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
)

// based on https://github.com/shirou/gopsutil/blob/master/net/net_linux.go

// GetConnections gets connections in the namespace of the given pid.
// root is the root of the /proc filesystem. If unset, it defaults to "/proc"
func GetConnections(root string, pid int) ([]monitor.Connection, error) {
	if root == "" {
		root = "/proc"
	}

	var out []monitor.Connection
	var result multierror.Error

	for _, k := range connectionKindTypes {
		filename := filepath.Join(root, strconv.Itoa(pid), k.filename)
		connections, err := processINET(filename, k)
		if err != nil {
			result = multierror.Append(result, err)
			continue
		}

		for _, c := range connections {
			out = append(out, c)
		}

	}

	return out, result.NilOrError()
}

func processINET(filename string, kind netConnectionKindType) ([]monitor.Connection, error) {
	contents, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to read file %q", filename)
	}

	var out []monitor.Connection
	lines := bytes.Split(contents, []byte("\n"))
	// skip first line
	for _, line := range lines[1:] {
		l := strings.Fields(string(line))
		if len(l) < 10 {
			continue
		}

		localAddress, err := decodeAddress(l[1])
		if err != nil {
			// just skip for now
			continue
		}

		remoteAddess, err := decodeAddress(l[2])
		if err != nil {
			// just skip for now
			continue
		}
		var c monitor.Connection

		c.Family = kind.family
		c.Type = kind.Type
		c.LocalAddress = localAddress
		c.RemoteAddess = remoteAddess

		status := ""
		if kind.sockType == "tcp" {
			status = _TCPStatuses[l[3]]
		}

		if status == "" {
			status = "NONE"
		}
		c.Status = status

		out = append(out, c)
	}

	return out, nil
}

type netConnectionKindType struct {
	family   string
	sockType string
	filename string
}

// TODO: ipv6
var connectionKindTypes = []netConnectionKindType{
	{"inet", "tcp", "tcp"},
	//{"inet6", "tcp", "tcp6"},
	{"inet", "udp", "udp"},
	//{"inet6", "udp", "udp6"},
}

var _TCPStatuses = map[string]string{
	"01": "ESTABLISHED",
	"02": "SYN_SENT",
	"03": "SYN_RECV",
	"04": "FIN_WAIT1",
	"05": "FIN_WAIT2",
	"06": "TIME_WAIT",
	"07": "CLOSE",
	"08": "CLOSE_WAIT",
	"09": "LAST_ACK",
	"0A": "LISTEN",
	"0B": "CLOSING",
}

func decodeAddress(family string, src string) (string, error) {
	t := strings.Split(src, ":")
	if len(t) != 2 {
		return "", errors.Errorf("does not contain port %q", src)
	}
	addr := t[0]
	port, err := strconv.ParseInt("0x"+t[1], 0, 64)
	if err != nil {
		return "", errors.Errorf("invalid port %q", src)
	}
	decoded, err := hex.DecodeString(addr)
	if err != nil {
		return "", errors.Wrapf(err, "decode error %q", addr)
	}
	var ip net.IP
	// Assumes this is little_endian??
	if family == "inet" {
		ip = net.IP(reverse(decoded))
	}

	return ip.String() + ":" + strconv.Itoa(port)

}

func reverse(s []byte) []byte {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
	return s
}
