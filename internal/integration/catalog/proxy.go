package catalog

import (
	"net/http"
	"net/url"
	"sync"
)

// ProxyPool manages a pool of proxies with round-robin rotation and blocking of
// unavailable proxies. It is safe for concurrent use.
type ProxyPool struct {
	mu      sync.Mutex
	proxies []string
	blocked map[string]bool
	index   int
}

// NewProxyPool creates a proxy pool from the given list of proxy URLs.
// If the list is empty, the pool operates in direct (no-proxy) mode.
func NewProxyPool(proxies []string) *ProxyPool {
	return &ProxyPool{
		proxies: proxies,
		blocked: make(map[string]bool),
	}
}

// Next returns the next available proxy URL using round-robin.
// Returns empty string if no proxies are configured or all are blocked.
func (p *ProxyPool) Next() string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return ""
	}

	checked := 0
	for checked < len(p.proxies) {
		proxy := p.proxies[p.index]
		p.index = (p.index + 1) % len(p.proxies)
		checked++

		if !p.blocked[proxy] {
			return proxy
		}
	}

	return "" // all blocked
}

// Block marks a proxy as blocked (e.g. after HTTP 403).
func (p *ProxyPool) Block(proxy string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked[proxy] = true
}

// AllBlocked returns true if every proxy in the pool is blocked or the pool is empty.
func (p *ProxyPool) AllBlocked() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.proxies) == 0 {
		return false // no proxies configured = direct mode, not "blocked"
	}
	for _, proxy := range p.proxies {
		if !p.blocked[proxy] {
			return false
		}
	}
	return true
}

// Reset unblocks all proxies. Useful for periodic recovery.
func (p *ProxyPool) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = make(map[string]bool)
}

// Transport returns an *http.Transport configured to use the given proxy URL.
// Returns nil transport (use default) if proxyURL is empty.
func ProxyTransport(proxyURL string) *http.Transport {
	if proxyURL == "" {
		return nil
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil
	}
	return &http.Transport{
		Proxy: http.ProxyURL(parsed),
	}
}
