package feeds

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"time"
)

// MaxBody caps a feed's size; a larger one is an error.
const MaxBody = 4 << 20

// Client fetches https feeds with an SSRF address guard.
type Client struct {
	ua  string
	cli *http.Client
}

// Result is one FetchAll outcome.
type Result struct {
	Entries []Entry
	Err     error
}

// NewClient returns a Client that refuses private dial targets.
func NewClient(ua string) *Client { return newClient(ua, guardDial, nil) }

// newClient builds a Client with the given dial guard (nil: none) and TLS
// config (nil: the system roots); tests pass an httptest server's.
func newClient(ua string, control func(string, string, syscall.RawConn) error, tlsCfg *tls.Config) *Client {
	return &Client{ua, &http.Client{
		Transport:     &http.Transport{DialContext: (&net.Dialer{Control: control}).DialContext, TLSClientConfig: tlsCfg},
		CheckRedirect: checkRedirect,
	}}
}

// checkRedirect allows at most 3 redirects, all to https.
func checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 4 {
		return fmt.Errorf("too many redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("redirect to non-https URL")
	}
	return nil
}

// guardDial refuses connections to addresses allowedIP rejects. It runs on
// the resolved address of every dial, redirects included, so DNS rebinding
// cannot get around it.
func guardDial(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !allowedIP(ip) {
		return fmt.Errorf("blocked address %s", host)
	}
	return nil
}

// allowedIP is false for loopback, private, link-local, unspecified,
// multicast, 0.0.0.0/8 and CGNAT (100.64/10) addresses, IPv4-mapped included.
func allowedIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	v4 := ip.To4()
	return v4 == nil || (v4[0] != 0 && (v4[0] != 100 || v4[1]&0xc0 != 64))
}

// Fetch downloads and parses one https feed URL.
func (c *Client) Fetch(ctx context.Context, rawURL string) ([]Entry, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("refusing non-https URL")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.ua)
	res, err := c.cli.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > MaxBody {
		return nil, fmt.Errorf("feed larger than 4 MiB")
	}
	return Parse(bytes.NewReader(b))
}

// FetchAll fetches urls with at most 4 in flight; results match urls order.
func (c *Client) FetchAll(ctx context.Context, urls []string) []Result {
	out, sem, wg := make([]Result, len(urls)), make(chan struct{}, 4), sync.WaitGroup{}
	for i, u := range urls {
		wg.Add(1)
		go func(i int, u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			out[i].Entries, out[i].Err = c.Fetch(ctx, u)
		}(i, u)
	}
	wg.Wait()
	return out
}
