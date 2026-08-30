// Package bank pushes harvested seeds to the Feed Runner backend.
//
// This is the only outbound write in an otherwise read-only tool, and it writes
// into a live personal account, so it is deliberately conservative: it is off
// unless switched on, it never invents a seed, and it treats "the server
// already has this" as success rather than as an error to retry forever.
//
// Delivery is at-least-once and made safe by the id. Every seed carries a
// client_seed_id of "harvest-<post id>", which the backend enforces as unique
// per account, so a push that succeeds server-side and then fails on the way
// back cannot create a second copy when the next run retries it.
package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
)

type Client struct {
	cfg  config.Bank
	log  *slog.Logger
	http *http.Client
}

func New(cfg config.Bank, log *slog.Logger) *Client {
	timeout := time.Duration(cfg.TimeoutSec) * time.Second
	if timeout <= 0 {
		// The backend sleeps on the free tier and a cold boot is ~30s, so a
		// short default here would fail every first push of the day.
		timeout = 60 * time.Second
	}
	return &Client{cfg: cfg, log: log, http: &http.Client{Timeout: timeout}}
}

// Result reports what a push run did.
type Result struct {
	Sent      int      // newly stored server-side
	Duplicate int      // already there; delivered, not an error
	Failed    int
	Delivered []string // client_seed_ids safe to stamp as posted
	// Fatal is set when the whole run should stop rather than keep trying: a
	// bad token fails identically for every remaining seed, and hammering an
	// auth endpoint fifty times is how you get rate limited.
	Fatal error
}

// authError means the token was rejected. Retrying will not help.
type authError struct{ body string }

func (e authError) Error() string {
	return "backend rejected the token (" + e.body + "): the session token is wrong or " +
		"was signed out. Get a fresh one by signing in on the phone, or set bank.enabled false"
}

// Push sends seeds one at a time, stopping early on an error that will repeat.
//
// One at a time rather than batched because the backend has no batch route and
// because a partial failure has to be knowable per seed: the whole point of the
// outbox is that seed 7 failing does not un-deliver seeds 1 to 6.
func (c *Client) Push(ctx context.Context, seeds []model.IdeaSeed) Result {
	var res Result
	for _, seed := range seeds {
		if ctx.Err() != nil {
			res.Fatal = ctx.Err()
			return res
		}
		duplicate, err := c.postSeed(ctx, seed)
		switch {
		case err == nil:
			if duplicate {
				res.Duplicate++
			} else {
				res.Sent++
			}
			res.Delivered = append(res.Delivered, seed.ID)
		default:
			res.Failed++
			c.log.Warn("seed push failed, staying queued for the next run",
				"seed", seed.ID, "err", err)
			if _, fatal := err.(authError); fatal {
				res.Fatal = err
				return res
			}
		}
	}
	return res
}

// Health reports whether the backend is answering at all, so a run can say
// "the bank is asleep" once instead of failing fifty times to discover it.
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}

// postSeed sends one seed and reports whether the backend already had it.
func (c *Client) postSeed(ctx context.Context, seed model.IdeaSeed) (bool, error) {
	payload, err := json.Marshal(seed)
	if err != nil {
		return false, err
	}

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, c.cfg.BaseURL+"/seeds", bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)

	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Capped: an error page from a proxy can be a whole HTML document, and none
	// of it belongs in a log line.
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))

	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return false, authError{body: serverMessage(body)}
	case resp.StatusCode == http.StatusUnprocessableEntity:
		// The seed itself is the problem, so retrying it next run just repeats
		// this. Report it as delivered-with-a-shrug rather than wedging the
		// outbox behind one bad row.
		c.log.Warn("backend rejected a seed as unusable, dropping it from the queue",
			"seed", seed.ID, "reason", serverMessage(body))
		return true, nil
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return false, fmt.Errorf("POST /seeds returned %d: %s",
			resp.StatusCode, serverMessage(body))
	}

	var answer struct {
		Duplicate bool `json:"duplicate"`
	}
	// A 200 we cannot parse still means stored; only the duplicate flag is lost.
	_ = json.Unmarshal(body, &answer)
	return answer.Duplicate, nil
}

// serverMessage pulls the backend's own error sentence out of a body, so the
// log says "seed is empty" rather than echoing a JSON blob.
func serverMessage(body []byte) string {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err == nil && payload.Error != "" {
		return payload.Error
	}
	trimmed := strings.TrimSpace(string(body))
	if len(trimmed) > 200 {
		return trimmed[:200] + "..."
	}
	return trimmed
}
