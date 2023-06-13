// Package loadbalance is a plugin for rewriting responses to do "load balancing"
package loadbalance

import (
	"context"

	"github.com/coredns/coredns/plugin"
	"github.com/coredns/coredns/request"

	"github.com/miekg/dns"
)

// RoundRobin is a plugin to rewrite responses for "load balancing".
type LoadBalance struct {
	Next    plugin.Handler
	shuffle func(*dns.Msg) *dns.Msg
	session *SessionLoadBalancer
}

// ServeDNS implements the plugin.Handler interface.
func (lb LoadBalance) ServeDNS(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	if lb.shuffle != nil {
		return lb.ServeShuffle(ctx, w, r)
	}
	return lb.ServeSession(ctx, w, r)
}

// ServeShuffle serves a request by shuffling results.
func (lb LoadBalance) ServeShuffle(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	rw := &LoadBalanceResponseWriter{ResponseWriter: w, shuffle: lb.shuffle}
	return plugin.NextOrFailure(lb.Name(), lb.Next, ctx, rw, r)
}

// ServeSession generates a response based on host session metrics.
func (lb LoadBalance) ServeSession(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) (int, error) {
	state := request.Request{W: w, Req: r}
	qname := state.Name()
	hostname, domain := split(qname)
	// log.Infof("BJORN ServeSession() hostname: '%s' domain: '%s'", hostname, domain)
	hostnameMatch := hostname == lb.session.hostname
	domainMatch := (lb.session.domain == "" || domain == lb.session.domain)

	// Initially, only support type A requests.
	if state.QType() != dns.TypeA {
		// log.Infof("Not handling request of type: %v", state.QType())
		return plugin.NextOrFailure(lb.Name(), lb.Next, ctx, w, r)
	}
	if hostnameMatch && domainMatch {
		ips := lb.session.GetIPs()
		answers := []dns.RR{}
		for _, ip := range ips {
			answers = append(answers, &dns.A{
				Hdr: dns.RR_Header{
					Name:   state.QName(),
					Rrtype: dns.TypeA,
					Class:  state.QClass(),
					Ttl:    1},
				A: ip,
			})
		}
		a := dns.Msg{Question: r.Question, Answer: answers}
		a.SetReply(r)
		a.Authoritative = true
		w.WriteMsg(&a)
		return 0, nil
	}

	log.Infof("Hostname / domain mismatch. hostname: %v domain: %v", hostname, domain)
	return plugin.NextOrFailure(lb.Name(), lb.Next, ctx, w, r)
}

// Name implements the Handler interface.
func (lb LoadBalance) Name() string { return "loadbalance" }
