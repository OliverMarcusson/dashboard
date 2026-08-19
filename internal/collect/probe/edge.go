package probe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/olivermarcusson/dashboard/internal/hub"
)

const TopicEdge = "probe.edge"

// Site is one hostname Caddy routes, with its certificate state.
type Site struct {
	Host      string `json:"host"`
	Issuer    string `json:"issuer,omitempty"`
	NotAfter  string `json:"not_after,omitempty"`
	DaysLeft  int    `json:"days_left"`
	Reachable bool   `json:"reachable"`
	Error     string `json:"error,omitempty"`
}

// Peer is one Tailscale node.
type Peer struct {
	Name   string `json:"name"`
	Online bool   `json:"online"`
	OS     string `json:"os,omitempty"`
	LastIP string `json:"last_ip,omitempty"`
}

type Edge struct {
	Sites     []Site `json:"sites"`
	PublicIP  string `json:"public_ip,omitempty"`
	Tailscale struct {
		Running bool   `json:"running"`
		Self    string `json:"self,omitempty"`
		Peers   []Peer `json:"peers"`
	} `json:"tailscale"`
	SoonestExpiry int       `json:"soonest_expiry_days"`
	At            time.Time `json:"at"`
	Error         string    `json:"error,omitempty"`
}

type EdgeProbe struct {
	hub        *hub.Hub
	caddyAdmin string
	interval   time.Duration
	client     *http.Client
}

func NewEdge(h *hub.Hub, caddyAdmin string, interval time.Duration) *EdgeProbe {
	if interval <= 0 {
		interval = time.Hour
	}
	if caddyAdmin == "" {
		caddyAdmin = "http://127.0.0.1:2019"
	}
	return &EdgeProbe{
		hub:        h,
		caddyAdmin: caddyAdmin,
		interval:   interval,
		client:     &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *EdgeProbe) Name() string { return "probe.edge" }

func (p *EdgeProbe) Run(ctx context.Context) error {
	return loop(ctx, p.interval, func() {
		p.hub.Publish(TopicEdge, p.Snapshot(ctx))
	})
}

func (p *EdgeProbe) Snapshot(ctx context.Context) Edge {
	e := Edge{At: time.Now().UTC(), Sites: []Site{}, SoonestExpiry: -1}
	e.Tailscale.Peers = []Peer{}

	hosts, err := p.hostnames(ctx)
	if err != nil {
		e.Error = err.Error()
	}

	// Certificates are checked concurrently but bounded — twenty sequential
	// TLS handshakes would make this probe take most of a minute.
	sem := make(chan struct{}, 6)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, host := range hosts {
		wg.Add(1)
		go func(host string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			site := checkCert(ctx, host)
			mu.Lock()
			e.Sites = append(e.Sites, site)
			mu.Unlock()
		}(host)
	}
	wg.Wait()

	// Nearest expiry first: that is the one that will break something.
	sort.Slice(e.Sites, func(i, j int) bool {
		a, b := e.Sites[i], e.Sites[j]
		if a.Reachable != b.Reachable {
			return !a.Reachable
		}
		if a.DaysLeft != b.DaysLeft {
			return a.DaysLeft < b.DaysLeft
		}
		return a.Host < b.Host
	})
	for _, s := range e.Sites {
		if s.Reachable && (e.SoonestExpiry < 0 || s.DaysLeft < e.SoonestExpiry) {
			e.SoonestExpiry = s.DaysLeft
		}
	}

	e.PublicIP = publicIP(ctx)
	p.tailscale(ctx, &e)
	return e
}

// hostnames reads every host Caddy routes from its admin API, so the site list
// is whatever is actually being served rather than a list kept in step by hand.
func (p *EdgeProbe) hostnames(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.caddyAdmin+"/config/", nil)
	if err != nil {
		return nil, err
	}
	res, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Caddy admin API unreachable: %w", err)
	}
	defer res.Body.Close()

	var cfg struct {
		Apps struct {
			HTTP struct {
				Servers map[string]struct {
					Routes []struct {
						Match []struct {
							Host []string `json:"host"`
						} `json:"match"`
					} `json:"routes"`
				} `json:"servers"`
			} `json:"http"`
		} `json:"apps"`
	}
	if err := json.NewDecoder(res.Body).Decode(&cfg); err != nil {
		return nil, err
	}

	seen := map[string]bool{}
	var hosts []string
	for _, server := range cfg.Apps.HTTP.Servers {
		for _, route := range server.Routes {
			for _, match := range route.Match {
				for _, host := range match.Host {
					// Wildcards cannot be dialled for a certificate.
					if host == "" || strings.HasPrefix(host, "*") || seen[host] {
						continue
					}
					seen[host] = true
					hosts = append(hosts, host)
				}
			}
		}
	}
	sort.Strings(hosts)
	return hosts, nil
}

// checkCert opens a TLS connection and reads the leaf certificate. It dials the
// local edge rather than the public address so the check works even when the
// hostname resolves elsewhere.
func checkCert(ctx context.Context, host string) Site {
	site := Site{Host: host}

	dialer := &net.Dialer{Timeout: 6 * time.Second}
	conn, err := tls.DialWithDialer(dialer, "tcp", net.JoinHostPort(host, "443"), &tls.Config{
		ServerName: host,
		// The certificate is being inspected, not trusted: an expired or
		// self-signed cert must still be reportable rather than a dial error.
		InsecureSkipVerify: true,
	})
	if err != nil {
		site.Error = strings.TrimPrefix(err.Error(), "tls: ")
		return site
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		site.Error = "the server presented no certificate"
		return site
	}
	leaf := certs[0]

	site.Reachable = true
	site.Issuer = leaf.Issuer.CommonName
	site.NotAfter = leaf.NotAfter.UTC().Format(time.RFC3339)
	site.DaysLeft = int(time.Until(leaf.NotAfter).Hours() / 24)
	return site
}

// publicIP asks Cloudflare, which this host already depends on for DNS.
func publicIP(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://cloudflare.com/cdn-cgi/trace", nil)
	if err != nil {
		return ""
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()

	body, err := io.ReadAll(io.LimitReader(res.Body, 4096))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if after, ok := strings.CutPrefix(line, "ip="); ok {
			return strings.TrimSpace(after)
		}
	}
	return ""
}

func (p *EdgeProbe) tailscale(ctx context.Context, e *Edge) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "tailscale", "status", "--json").Output()
	if err != nil {
		return
	}
	var status struct {
		BackendState string `json:"BackendState"`
		Self         struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
		Peer map[string]struct {
			DNSName      string   `json:"DNSName"`
			Online       bool     `json:"Online"`
			OS           string   `json:"OS"`
			TailscaleIPs []string `json:"TailscaleIPs"`
		} `json:"Peer"`
	}
	if err := json.Unmarshal(out, &status); err != nil {
		return
	}

	e.Tailscale.Running = status.BackendState == "Running"
	e.Tailscale.Self = strings.TrimSuffix(status.Self.DNSName, ".")
	for _, peer := range status.Peer {
		p := Peer{
			Name:   strings.TrimSuffix(peer.DNSName, "."),
			Online: peer.Online,
			OS:     peer.OS,
		}
		if len(peer.TailscaleIPs) > 0 {
			p.LastIP = peer.TailscaleIPs[0]
		}
		e.Tailscale.Peers = append(e.Tailscale.Peers, p)
	}
	sort.Slice(e.Tailscale.Peers, func(i, j int) bool {
		a, b := e.Tailscale.Peers[i], e.Tailscale.Peers[j]
		if a.Online != b.Online {
			return a.Online
		}
		return a.Name < b.Name
	})
}
