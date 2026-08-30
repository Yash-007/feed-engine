package bank

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
)

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

func clientFor(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return New(config.Bank{
		Enabled: true, BaseURL: server.URL, Token: "fr_test", TimeoutSec: 5,
	}, quiet()), server
}

func seed(id string) model.IdeaSeed {
	return model.IdeaSeed{
		ID: id, Source: "harvest", Platform: "x", Category: "repost",
		Tension: "a pattern", AngleHint: "the layer",
		SourcePostURL: "https://x.com/someone/status/1", Visual: true,
	}
}

func TestPushSendsTheWireFormat(t *testing.T) {
	var got map[string]any
	var auth string

	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"seed":{},"duplicate":false}`))
	})

	res := client.Push(context.Background(), []model.IdeaSeed{seed("harvest-1")})
	if res.Sent != 1 || res.Failed != 0 {
		t.Fatalf("got %+v, want one sent", res)
	}
	if auth != "Bearer fr_test" {
		t.Errorf("Authorization = %q", auth)
	}

	// The fields the backend needs are exactly the ones added for the repost
	// and image flows; a rename on either side has to fail here.
	for _, key := range []string{
		"client_seed_id", "source", "platform", "category",
		"tension", "angle_hint", "source_post_url", "visual",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("payload is missing %q: %v", key, got)
		}
	}
	if got["client_seed_id"] != "harvest-1" {
		t.Errorf("client_seed_id = %v", got["client_seed_id"])
	}
	if got["visual"] != true {
		t.Errorf("visual = %v, want true", got["visual"])
	}
}

// A seed the server already holds is delivered. Treating it as a failure would
// leave it unstamped and re-push it on every run forever.
func TestPushCountsDuplicatesAsDelivered(t *testing.T) {
	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"seed":{},"duplicate":true}`))
	})

	res := client.Push(context.Background(), []model.IdeaSeed{seed("harvest-2")})
	if res.Duplicate != 1 || res.Sent != 0 {
		t.Fatalf("got %+v, want one duplicate", res)
	}
	if len(res.Delivered) != 1 || res.Delivered[0] != "harvest-2" {
		t.Errorf("a duplicate must still be stamped: %v", res.Delivered)
	}
}

// A bad token fails identically for every remaining seed, so the run stops
// rather than hammering an auth-guarded endpoint fifty times.
func TestPushStopsOnAuthFailure(t *testing.T) {
	calls := 0
	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"unauthorized"}`))
	})

	res := client.Push(context.Background(), []model.IdeaSeed{
		seed("harvest-3"), seed("harvest-4"), seed("harvest-5"),
	})
	if calls != 1 {
		t.Errorf("made %d calls, want 1 before giving up", calls)
	}
	if res.Fatal == nil {
		t.Error("a rejected token must be fatal to the push")
	}
	if len(res.Delivered) != 0 {
		t.Errorf("nothing was delivered: %v", res.Delivered)
	}
}

// One unusable seed must not wedge the queue behind it forever.
func TestPushDropsSeedsTheServerCallsUnusable(t *testing.T) {
	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"seed is empty"}`))
	})

	res := client.Push(context.Background(), []model.IdeaSeed{seed("harvest-6")})
	if res.Failed != 0 {
		t.Errorf("a 422 is not a retryable failure: %+v", res)
	}
	if len(res.Delivered) != 1 {
		t.Errorf("it must leave the queue: %v", res.Delivered)
	}
}

// A transient server error keeps the seed queued and keeps going.
func TestPushKeepsGoingAfterAServerError(t *testing.T) {
	calls := 0
	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.Write([]byte(`{"duplicate":false}`))
	})

	res := client.Push(context.Background(), []model.IdeaSeed{seed("a"), seed("b")})
	if res.Failed != 1 || res.Sent != 1 {
		t.Fatalf("got %+v, want one failed and one sent", res)
	}
	if len(res.Delivered) != 1 || res.Delivered[0] != "b" {
		t.Errorf("only the successful seed is delivered: %v", res.Delivered)
	}
	if res.Fatal != nil {
		t.Errorf("a 502 is transient, not fatal: %v", res.Fatal)
	}
}

func TestHealth(t *testing.T) {
	client, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("probed %q, want /healthz", r.URL.Path)
		}
		w.Write([]byte(`{"ok":true}`))
	})
	if err := client.Health(context.Background()); err != nil {
		t.Errorf("Health: %v", err)
	}

	down, _ := clientFor(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if err := down.Health(context.Background()); err == nil {
		t.Error("a 503 must be reported as unhealthy")
	}
}

func TestServerMessage(t *testing.T) {
	if got := serverMessage([]byte(`{"error":"seed is empty"}`)); got != "seed is empty" {
		t.Errorf("got %q", got)
	}
	if got := serverMessage([]byte("plain text")); got != "plain text" {
		t.Errorf("got %q", got)
	}
	long := make([]byte, 500)
	for i := range long {
		long[i] = 'x'
	}
	if got := serverMessage(long); len(got) > 210 {
		t.Errorf("a long body must be capped, got %d chars", len(got))
	}
}
