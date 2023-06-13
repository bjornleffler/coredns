package loadbalance

import (
	"math/rand"
	"net"
	"net/netip"
	"time"
)

const (
	// Scrape targets every 15s.
	// Remove host from active if unavailable for 30+ seconds.
	DefaultScrapeSeconds  = 15
	DefaultTimeoutSeconds = 30
)

type SessionManager struct {
	scrapeMetric          string
	scrapePort            uint16
	scrapeTimeoutSeconds  uint
	scrapeIntervalSeconds uint
	hosts                 map[netip.Addr]*Host
}

type Host struct {
	ip netip.Addr
	// Prometheus port and metric name scrape.
	port uint16
	// Scraped base value and last update time.
	base    float32
	updated time.Time
	// Current estimated value.
	estimate float32
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		scrapeTimeoutSeconds:  DefaultTimeoutSeconds,
		scrapeIntervalSeconds: DefaultScrapeSeconds,
		hosts:                 make(map[netip.Addr]*Host),
	}
}

func (sm *SessionManager) Scrape(host *Host) {
	for {
		log.Infof("Scrape host %v", host.ip)
		start := time.Now()
		// TODO(leffler): Scraping code.
		timeLeft := float64(sm.scrapeIntervalSeconds) - time.Since(start).Seconds()
		time.Sleep(time.Duration(timeLeft) * time.Second)
	}
}

func (sm *SessionManager) Add(addr netip.Addr) {
	if _, ok := sm.hosts[addr]; ok {
		return
	}
	host := &Host{
		ip:       addr,
		port:     0,
		updated:  time.Unix(0, 0),
		base:     0,
		estimate: 0,
	}
	sm.hosts[addr] = host
}

func (sm *SessionManager) Start() {
	for _, host := range sm.hosts {
		// Set defaults.
		host.port = sm.scrapePort
		// Start scraping hosts.
		go sm.Scrape(host)
	}
}

func shuffle(ips []net.IP) {
	rand.Seed(time.Now().UnixNano())
	rand.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
}

func (sm *SessionManager) GetIPs() []net.IP {
	active := []net.IP{}
	all := []net.IP{}
	for ip, host := range sm.hosts {
		all = append(all, net.IP(ip.AsSlice()))
		if time.Since(host.updated).Seconds() < float64(sm.scrapeTimeoutSeconds) {
			active = append(active, net.IP(ip.AsSlice()))
		}
	}
	if len(active) == 0 {
		log.Infof("No active hosts. Return all known ips, shuffled.")
		shuffle(all)
		return all
	}
	// TODO(leffler): Order IPs.
	return active
}

// TODO(leffler): Used for debugging. Remove.
func (sm *SessionManager) ListIPs() []string {
	ips := []string{}
	for ip, _ := range sm.hosts {
		ips = append(ips, ip.String())
	}
	return ips
}
