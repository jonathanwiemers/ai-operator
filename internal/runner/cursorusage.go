package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// cursorAPI is Cursor's dashboard API (Connect RPC with JSON). It is
// undocumented: cursor-agent has no usage command, so the runner calls what
// the CLI and the dashboard call.
const cursorAPI = "https://api2.cursor.sh"

// cursorRecheck is how long a slow-pool answer is kept.
const cursorRecheck = 10 * time.Minute

// CursorUsage tells whether Cursor serves the account from its slow pool:
// past the plan's usage every request waits seconds, and no error says so.
// A nil CursorUsage, or any error, counts as "not slow".
type CursorUsage struct {
	Base, Key string
	Client    *http.Client

	mu      sync.Mutex
	checked time.Time
	slow    bool
	reset   time.Time
}

// NewCursorUsage reads Cursor's usage with an API key; nil without one.
func NewCursorUsage(key string) *CursorUsage {
	if key == "" {
		return nil
	}
	return &CursorUsage{Base: cursorAPI, Key: key, Client: &http.Client{Timeout: 10 * time.Second}}
}

// slowPool reports whether the account is in the slow pool and when its
// billing cycle ends (zero if unknown). Answers are kept for cursorRecheck;
// err is set only on the call that failed to ask.
func (c *CursorUsage) slowPool(ctx context.Context, now time.Time) (slow bool, reset time.Time, err error) {
	if c == nil {
		return false, time.Time{}, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checked.IsZero() && now.Sub(c.checked) < cursorRecheck {
		return c.slow, c.reset, nil
	}
	c.checked, c.slow, c.reset = now, false, time.Time{}
	var tok struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.post(ctx, "/auth/exchange_user_api_key", c.Key, &tok); err != nil {
		return false, time.Time{}, err
	}
	var policy struct {
		IsInSlowPool bool `json:"isInSlowPool"`
	}
	if err := c.post(ctx, "/aiserver.v1.DashboardService/GetUsageLimitPolicyStatus", tok.AccessToken, &policy); err != nil || !policy.IsInSlowPool {
		return false, time.Time{}, err
	}
	c.slow = true
	var usage struct {
		BillingCycleEnd string `json:"billingCycleEnd"` // Unix ms, as a string
	}
	if c.post(ctx, "/aiserver.v1.DashboardService/GetCurrentPeriodUsage", tok.AccessToken, &usage) == nil {
		if ms, perr := strconv.ParseInt(usage.BillingCycleEnd, 10, 64); perr == nil {
			c.reset = time.UnixMilli(ms)
		}
	}
	return c.slow, c.reset, nil
}

func (c *CursorUsage) post(ctx context.Context, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Base+path, bytes.NewReader([]byte("{}")))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connect-Protocol-Version", "1")
	resp, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("cursor %s: %s", path, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
