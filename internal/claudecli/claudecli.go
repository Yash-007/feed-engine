// Package claudecli shells out to `claude -p`, feeding posts in as JSON on
// stdin and parsing a JSON array of idea seeds back out.
//
// Claude is a language model behind a CLI, not an API with a schema guarantee:
// it can wrap the array in prose or a code fence. So output goes through a
// salvage pass first, and the whole call is retried once with a stricter
// reminder if the salvage still doesn't parse.
package claudecli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Yash-007/feed-engine/internal/config"
	"github.com/Yash-007/feed-engine/internal/model"
)

const retryNudge = "\n\nIMPORTANT: your previous reply was not parseable. " +
	"Reply with a JSON array and nothing else — no prose, no markdown fence, no commentary. " +
	"An empty array [] is a valid answer."

type Client struct {
	cfg config.Claude
	log *slog.Logger
}

func New(cfg config.Claude, log *slog.Logger) *Client { return &Client{cfg: cfg, log: log} }

// postIn is the trimmed view of a post that goes over stdin. Sending the full
// struct would waste tokens on fields the prompt has no use for.
type postIn struct {
	PostID    string `json:"post_id"`
	Author    string `json:"author"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp"`
	WordCount int    `json:"word_count"`
	ImagePath string `json:"image_path,omitempty"`
}

func trim(posts []model.Post, withImage bool) []postIn {
	out := make([]postIn, 0, len(posts))
	for _, p := range posts {
		in := postIn{
			PostID: p.ID, Author: p.Author, Text: p.Text,
			Timestamp: p.Timestamp, WordCount: p.WordCount,
		}
		if withImage {
			if abs, err := absPath(p.ImagePath); err == nil {
				in.ImagePath = abs
			} else {
				in.ImagePath = p.ImagePath
			}
		}
		out = append(out, in)
	}
	return out
}

func absPath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty path")
	}
	return filepath.Abs(p)
}

// SeedText runs the text batch(es). Posts are chunked by config.TextBatchSize.
func (c *Client) SeedText(ctx context.Context, posts []model.Post) ([]model.SeedResponse, error) {
	return c.seed(ctx, posts, c.cfg.TextPrompt, c.cfg.TextBatchSize, c.cfg.ExtraArgs, false)
}

// SeedVisual runs the vision batch(es). Screenshot paths ride along in the JSON;
// the prompt tells Claude to open them with Read, which is why the visual call
// gets --allowedTools Read.
func (c *Client) SeedVisual(ctx context.Context, posts []model.Post) ([]model.SeedResponse, error) {
	return c.seed(ctx, posts, c.cfg.VisualPrompt, c.cfg.VisualBatchSize, c.cfg.VisualExtraArgs, true)
}

func (c *Client) seed(ctx context.Context, posts []model.Post, promptPath string, batch int, extra []string, withImage bool) ([]model.SeedResponse, error) {
	if len(posts) == 0 {
		return nil, nil
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		return nil, fmt.Errorf("read prompt %s: %w", promptPath, err)
	}
	if batch <= 0 {
		batch = len(posts)
	}

	var all []model.SeedResponse
	for i := 0; i < len(posts); i += batch {
		end := min(i+batch, len(posts))
		chunk := posts[i:end]

		payload, err := json.Marshal(trim(chunk, withImage))
		if err != nil {
			return all, err
		}

		seeds, err := c.callWithRetry(ctx, string(prompt), payload, extra)
		if err != nil {
			// One bad batch shouldn't sink the run — log it and keep going.
			c.log.Error("claude batch failed, skipping it", "batch_start", i, "size", len(chunk), "err", err)
			continue
		}
		c.log.Info("claude batch done", "sent", len(chunk), "seeds", len(seeds), "visual", withImage)
		all = append(all, seeds...)
	}
	return all, nil
}

func (c *Client) callWithRetry(ctx context.Context, prompt string, payload []byte, extra []string) ([]model.SeedResponse, error) {
	out, err := c.exec(ctx, prompt, payload, extra)
	if err == nil {
		if seeds, perr := parseSeeds(out); perr == nil {
			return seeds, nil
		} else {
			c.log.Warn("claude output did not parse, retrying once", "err", perr, "head", head(out, 200))
		}
	} else {
		c.log.Warn("claude invocation failed, retrying once", "err", err)
	}

	out, err = c.exec(ctx, prompt+retryNudge, payload, extra)
	if err != nil {
		return nil, err
	}
	seeds, perr := parseSeeds(out)
	if perr != nil {
		return nil, fmt.Errorf("%w (output head: %s)", perr, head(out, 300))
	}
	return seeds, nil
}

func (c *Client) exec(ctx context.Context, prompt string, stdin []byte, extra []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.cfg.TimeoutSec)*time.Second)
	defer cancel()

	args := append([]string{"-p", prompt}, extra...)
	cmd := exec.CommandContext(ctx, c.cfg.Bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("claude timed out after %ds", c.cfg.TimeoutSec)
		}
		return "", fmt.Errorf("claude exited badly: %w (stderr: %s)", err, head(stderr.String(), 300))
	}
	return stdout.String(), nil
}

// parseSeeds accepts a bare array, a fenced array, or an array buried in prose.
func parseSeeds(out string) ([]model.SeedResponse, error) {
	s := strings.TrimSpace(out)
	if s == "" {
		return nil, fmt.Errorf("empty output")
	}
	if seeds, err := decode(s); err == nil {
		return seeds, nil
	}
	if body, ok := unfence(s); ok {
		if seeds, err := decode(body); err == nil {
			return seeds, nil
		}
	}
	// Last resort: widest [ ... ] span in the output.
	lo, hi := strings.Index(s, "["), strings.LastIndex(s, "]")
	if lo >= 0 && hi > lo {
		if seeds, err := decode(s[lo : hi+1]); err == nil {
			return seeds, nil
		}
	}
	return nil, fmt.Errorf("no JSON array found in claude output")
}

func decode(s string) ([]model.SeedResponse, error) {
	var seeds []model.SeedResponse
	dec := json.NewDecoder(strings.NewReader(strings.TrimSpace(s)))
	if err := dec.Decode(&seeds); err != nil {
		return nil, err
	}
	return seeds, nil
}

func unfence(s string) (string, bool) {
	i := strings.Index(s, "```")
	if i < 0 {
		return "", false
	}
	rest := s[i+3:]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[nl+1:] // drop the language tag line
	}
	if j := strings.Index(rest, "```"); j >= 0 {
		return rest[:j], true
	}
	return rest, true
}

func head(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
