package loadbalance

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"path/filepath"
	"strconv"
	"time"

	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	clog "github.com/coredns/coredns/plugin/pkg/log"
	"golang.org/x/exp/slices"

	"github.com/miekg/dns"
)

var log = clog.NewWithPlugin("loadbalance")
var errOpen = errors.New("Weight file open error")

func init() { plugin.Register("loadbalance", setup) }

type lbFuncs struct {
	shuffleFunc func(*dns.Msg) *dns.Msg
	// TODO(leffler): Move to LoadBalance struct.
	onStartUpFunc  func() error
	onShutdownFunc func() error
	weighted       *weightedRR // used in unit tests only
}

func setup(c *caddy.Controller) error {
	//shuffleFunc, startUpFunc, shutdownFunc, err := parse(c)
	lb, session, err := parse(c)
	if err != nil {
		return plugin.Error("loadbalance", err)
	}
	if session != nil {
		dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
			return LoadBalance{Next: next, shuffle: nil, session: session}
		})
		return nil
	}

	if lb.onStartUpFunc != nil {
		c.OnStartup(lb.onStartUpFunc)
	}
	if lb.onShutdownFunc != nil {
		c.OnShutdown(lb.onShutdownFunc)
	}
	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return LoadBalance{Next: next, shuffle: lb.shuffleFunc, session: nil}
	})
	return nil
}

// func parse(c *caddy.Controller) (string, *weightedRR, error) {
func parse(c *caddy.Controller) (*lbFuncs, *SessionLoadBalancer, error) {
	for c.Next() {
		args := c.RemainingArgs()
		if len(args) == 0 {
			lb, err := parseRandomShuffle(c, args)
			return lb, nil, err
		}
		switch args[0] {
		case ramdomShufflePolicy:
			lb, err := parseRandomShuffle(c, args)
			return lb, nil, err
		case weightedRoundRobinPolicy:
			lb, err := parseRandomShuffle(c, args)
			return lb, nil, err
		case sessionPolicy:
			session, err := parseSession(c, args)
			return nil, session, err
		default:
			return nil, nil, fmt.Errorf("unknown policy: %s", args[0])
		}
	}
	return nil, nil, c.ArgErr()
}

func parseRandomShuffle(c *caddy.Controller, args []string) (*lbFuncs, error) {
	if len(args) > 1 {
		return nil, c.Errf("unknown property for %s", args[0])
	}
	return &lbFuncs{shuffleFunc: randomShuffle}, nil
}

func parseWeightedRoundRobin(c *caddy.Controller, args []string) (*lbFuncs, error) {
	config := dnsserver.GetConfig(c)
	if len(args) < 2 {
		return nil, c.Err("missing weight file argument")
	}

	if len(args) > 2 {
		return nil, c.Err("unexpected argument(s)")
	}

	weightFileName := args[1]
	if !filepath.IsAbs(weightFileName) && config.Root != "" {
		weightFileName = filepath.Join(config.Root, weightFileName)
	}
	reload := 30 * time.Second // default reload period
	for c.NextBlock() {
		switch c.Val() {
		case "reload":
			t := c.RemainingArgs()
			if len(t) < 1 {
				return nil, c.Err("reload duration value is missing")
			}
			if len(t) > 1 {
				return nil, c.Err("unexpected argument")
			}
			var err error
			reload, err = time.ParseDuration(t[0])
			if err != nil {
				return nil, c.Errf("invalid reload duration '%s'", t[0])
			}
		default:
			return nil, c.Errf("unknown property '%s'", c.Val())
		}
	}
	return createWeightedFuncs(weightFileName, reload), nil
}

func checkSessionInputs(c *caddy.Controller, key string, args []string) error {
	singleInputKeys := []string{
		sessionDomain,
		sessionScrapeMetric,
		sessionScrapePort,
		sessionScrapeTimeout}
	multipleInputKeys := []string{sessionTargetIps}
	numericInputKeys := []string{
		sessionScrapePort,
		sessionScrapeTimeout}
	if slices.Contains(singleInputKeys, key) {
		if len(args) != 1 {
			return c.Err("Expected single parameters for " + key)
		}
	}
	if slices.Contains(multipleInputKeys, key) {
		if len(args) == 0 {
			return c.Err("Expected 1+ parameters for " + key)
		}
	}
	if slices.Contains(numericInputKeys, key) {
		value := args[0]
		_, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			msg := fmt.Sprintf("Failed to parse %s: %v is not a number",
				key, value)
			return c.Err(msg)
		}
	}
	return nil
}

func parseSession(c *caddy.Controller, args []string) (*SessionLoadBalancer, error) {
	if len(args) != 2 {
		msg := fmt.Sprintf("Expected 'session' and hostname parameters. Got: %v", args)
		return nil, c.Err(msg)
	}
	session := NewSessionLoadBalancer()
	session.hostname = args[1]
	for c.NextBlock() {
		key := c.Val()
		args := c.RemainingArgs()
		checkSessionInputs(c, key, args)
		value := args[0]
		i, _ := strconv.ParseInt(value, 10, 32)
		switch key {
		case sessionTargetIps:
			ips, err := parseTargetIps(args)
			if err != nil {
				return nil, c.Err(fmt.Sprintf("%v", err))
			}
			for _, ip := range ips {
				session.manager.Add(ip)
			}
		case sessionDomain:
			session.domain = value
		case sessionScrapeMetric:
			session.manager.scrapeMetric = value
		case sessionScrapePort:
			session.manager.scrapePort = uint16(i)
		case sessionScrapeTimeout:
			session.manager.scrapeTimeoutSeconds = uint(i)
		default:
			return nil, c.Err("Unknown parameter: " + key)
		}
	}
	session.manager.Start()
	session.PrintConfig()
	return session, nil
}

// TODO(leffler): Move the functions below to some utility function or file.

func increment(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] > 0 {
			break
		}
	}
}

func expandNetworkPrefix(prefix string) (addrs []netip.Addr, err error) {
	ipv4, ipv4Net, err := net.ParseCIDR(prefix)
	if err != nil {
		return addrs, err
	}

	for ip := ipv4.Mask(ipv4Net.Mask); ipv4Net.Contains(ip); increment(ip) {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			addrs = append(addrs, addr)
		}
	}
	return addrs, nil
}

func parseTargetIps(prefixes []string) ([]netip.Addr, error) {
	addrs := []netip.Addr{}
	for _, prefix := range prefixes {
		// Try parse as single IP: a.b.c.d
		ip, err := netip.ParseAddr(prefix)
		if err == nil {
			addrs = append(addrs, ip)
			continue
		}
		// If that didn't work, try parsing as cidr: a.b.c.d/e
		ips, err := expandNetworkPrefix(prefix)
		if err != nil {
			log.Infof("Error: %v", err)
			return addrs, err
		}
		addrs = append(addrs, ips...)
	}
	return addrs, nil
}
