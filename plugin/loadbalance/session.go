// Session based load balancing.
package loadbalance

import (
	"net"
	"strings"
)

const (
	sessionPolicy        = "session"
	sessionTargetIps     = "session_target_ips"
	sessionDomain        = "session_domain"
	sessionScrapeMetric  = "session_scrape_metric"
	sessionScrapePort    = "session_scrape_port"
	sessionScrapeTimeout = "session_scrape_timeout"
)

// SessionLoadBalancer "load balances" answers based on (tcp) session count on the target hosts.
type SessionLoadBalancer struct {
	hostname string
	domain   string
	manager  *SessionManager
}

type PrometheusConfig struct {
	port uint16
}

func NewSessionLoadBalancer() *SessionLoadBalancer {
	return &SessionLoadBalancer{
		hostname: "",
		domain:   "",
		manager:  NewSessionManager(),
	}
}

func (s *SessionLoadBalancer) PrintConfig() {
	log.Infof("Hostname: %v", s.hostname)
	log.Infof("Domain: %v", s.domain)
	log.Infof("Target IPs: %v", s.manager.ListIPs())
	log.Infof("Scrape Metric: %v", s.manager.scrapeMetric)
	log.Infof("Scrape Port: %v", s.manager.scrapePort)
	log.Infof("Scrape Interval: %v seconds", s.manager.scrapeIntervalSeconds)
	log.Infof("Scrape Timeout: %v seconds", s.manager.scrapeTimeoutSeconds)
}

func split(fqdn string) (hostname, domain string) {
	names := strings.Split(strings.TrimSuffix(fqdn, "."), ".")
	if len(names) > 0 {
		hostname = names[0]
	}
	if len(names) > 1 {
		domain = strings.Join(names[1:], ".")
	}
	return
}

func (s *SessionLoadBalancer) GetIPs() []net.IP {
	return s.manager.GetIPs()
}
