package loadbalance

import (
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
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
	active                map[netip.Addr]*Host
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

func (host *Host) Update(value float32) {
	host.base = value
	host.estimate = value
	host.updated = time.Now()
}

// Active returns true if host was updated in the last <interval> seconds.
func (host *Host) Active(interval uint) bool {
	return time.Since(host.updated).Seconds() < float64(interval)
}

func NewSessionManager() *SessionManager {
	return &SessionManager{
		scrapeTimeoutSeconds:  DefaultTimeoutSeconds,
		scrapeIntervalSeconds: DefaultScrapeSeconds,
		hosts:                 make(map[netip.Addr]*Host),
		active:                make(map[netip.Addr]*Host),
	}
}

// getMetricValue is a helper function to extract the value from a metric.
func getMetricValue(mf *dto.MetricFamily) (float64, error) {
	switch {
	case mf.GetType() == dto.MetricType_GAUGE:
		gauge := mf.GetMetric()[0].GetGauge()
		return *gauge.Value, nil
	case mf.GetType() == dto.MetricType_COUNTER:
		counter := mf.GetMetric()[0].GetCounter()
		return *counter.Value, nil
	default:
		return 0, fmt.Errorf("Unsupported metric type: %v", mf)
	}
}

func (sm *SessionManager) ScrapeLoop(host *Host) {
	for {
		start := time.Now()
		sm.Scrape(host)
		// Update active host status.
		if host.Active(sm.scrapeTimeoutSeconds) {
			log.Infof("Add %v to active list.", host.ip)
			sm.active[host.ip] = host
		} else {
			log.Infof("Remove %v from active list.", host.ip)
			delete(sm.active, host.ip)
		}
		timeLeft := float64(sm.scrapeIntervalSeconds) - time.Since(start).Seconds()
		time.Sleep(time.Duration(timeLeft) * time.Second)
	}
}

func (sm *SessionManager) Scrape(host *Host) {
	url := fmt.Sprintf("http://%s:%d/metrics", host.ip, host.port)
	client := http.Client{
		Timeout: 10 * time.Second,
	}
	resp, err := client.Get(url)
	if err == nil {
		var parser expfmt.TextParser
		metrics, err := parser.TextToMetricFamilies(resp.Body)
		if err != nil {
			log.Errorf("Failed to parse metrics. err: %v", err)
		}
		for k, mf := range metrics {
			if k == sm.scrapeMetric {
				value, err := getMetricValue(mf)
				if err != nil {
					log.Errorf("%v", err)
					continue
				}
				host.Update(float32(value))

			}
		}
	} else {
		log.Errorf("Failed to get metrics. host: %s err: %v", host.ip, err)
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
		go sm.ScrapeLoop(host)
	}
}

// Sorting logic for list of hosts, by estimated number of connections.
type byEstimated []*Host

func (s byEstimated) Len() int {
	return len(s)
}
func (s byEstimated) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
func (s byEstimated) Less(i, j int) bool {
	return s[i].estimate < s[j].estimate
}

func (sm *SessionManager) GetIPs() []net.IP {
	active := []*Host{}
	for _, host := range sm.active {
		active = append(active, host)
	}
	if len(active) == 0 {
		log.Infof("No active hosts. Return all known ips, shuffled.")
		ips := []net.IP{}
		for ip, _ := range sm.hosts {
			ips = append(ips, net.IP(ip.AsSlice()))
		}
		rand.Seed(time.Now().UnixNano())
		rand.Shuffle(len(ips), func(i, j int) { ips[i], ips[j] = ips[j], ips[i] })
		return ips

	}
	// Sort active hosts by estimated number of connections.
	ips := []net.IP{}
	sort.Sort(byEstimated(active))
	for _, host := range active {
		ips = append(ips, net.IP(host.ip.AsSlice()))
	}
	// Increment estimated value for first host.
	active[0].estimate++
	return ips
}

// TODO(leffler): Used for debugging. Remove.
func (sm *SessionManager) PrintState() {
	log.Infof("Current active state:")
	for _, host := range sm.active {
		log.Infof(" - Host: %v estimate: %v", host.ip, host.estimate)
	}
}

// TODO(leffler): Used for debugging. Remove.
func (sm *SessionManager) ListIPs() []string {
	ips := []string{}
	for ip, _ := range sm.hosts {
		ips = append(ips, ip.String())
	}
	return ips
}
