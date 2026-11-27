package extras

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// Koolo is everything the extras may ask of Koolo. It is only reachable through Koolo's MCP server,
// so nothing an extra does can crash Koolo.
type Koolo interface {
	// Call runs a tool and returns its text output.
	Call(ctx context.Context, tool string, args map[string]any) (string, error)
	// Image runs a tool that returns an image (e.g. koolo_screenshot) and returns the JPEG bytes.
	Image(ctx context.Context, tool string, args map[string]any) ([]byte, error)
}

var ErrKooloDown = errors.New("koolo is not reachable (not running?)")

// MCPKoolo connects lazily and reconnects after errors.
type MCPKoolo struct {
	URL     string
	mu      sync.Mutex
	session *mcp.ClientSession
}

func (k *MCPKoolo) conn(ctx context.Context) (*mcp.ClientSession, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.session != nil {
		return k.session, nil
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "koolo-extras", Version: "1"}, nil)
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	s, err := client.Connect(cctx, &mcp.StreamableClientTransport{Endpoint: k.URL, MaxRetries: -1}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKooloDown, err)
	}
	k.session = s
	return s, nil
}

func (k *MCPKoolo) drop() {
	k.mu.Lock()
	if k.session != nil {
		_ = k.session.Close()
		k.session = nil
	}
	k.mu.Unlock()
}

func (k *MCPKoolo) call(ctx context.Context, tool string, args map[string]any) (*mcp.CallToolResult, error) {
	s, err := k.conn(ctx)
	if err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	res, err := s.CallTool(cctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		k.drop()
		return nil, fmt.Errorf("%s: %w", tool, err)
	}
	if res.IsError {
		return res, fmt.Errorf("%s: %s", tool, textOf(res))
	}
	return res, nil
}

func textOf(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(t.Text)
		}
	}
	return sb.String()
}

func (k *MCPKoolo) Call(ctx context.Context, tool string, args map[string]any) (string, error) {
	res, err := k.call(ctx, tool, args)
	if err != nil {
		return "", err
	}
	return textOf(res), nil
}

func (k *MCPKoolo) Image(ctx context.Context, tool string, args map[string]any) ([]byte, error) {
	res, err := k.call(ctx, tool, args)
	if err != nil {
		return nil, err
	}
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			return img.Data, nil
		}
	}
	return nil, fmt.Errorf("%s returned no image: %s", tool, textOf(res))
}

// ---------------------------------------------------------------------------
// Status types (the JSON koolo_status and koolo_metrics return)
// ---------------------------------------------------------------------------

type CharStatus struct {
	Status   string `json:"status"`
	Details  string `json:"details"`
	Games    int    `json:"games"`
	Deaths   int    `json:"deaths"`
	Chickens int    `json:"chickens"`
	Errors   int    `json:"errors"`
	Drops    int    `json:"drops"`
	LastEnd  string `json:"lastGameResult"`
}

type KooloStatus struct {
	Version    string                `json:"version"`
	Characters map[string]CharStatus `json:"characters"`
}

type CharMetrics struct {
	InGame     bool   `json:"inGame"`
	Level      int    `json:"level"`
	Experience int    `json:"experience"`
	Gold       int    `json:"gold"`
	Area       string `json:"area"`
	LifePct    int    `json:"lifePct"`
}

func GetStatus(ctx context.Context, k Koolo) (KooloStatus, error) {
	var st KooloStatus
	txt, err := k.Call(ctx, "koolo_status", nil)
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal([]byte(txt), &st)
}

func GetMetrics(ctx context.Context, k Koolo) (map[string]CharMetrics, error) {
	out := map[string]CharMetrics{}
	txt, err := k.Call(ctx, "koolo_metrics", nil)
	if err != nil {
		return nil, err
	}
	return out, json.Unmarshal([]byte(txt), &out)
}

// ---------------------------------------------------------------------------
// Runner: running programs (Claude Code, git, the rebuild script). Faked in tests.
// ---------------------------------------------------------------------------

type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) (string, error)
	Start(dir, name string, args ...string) error // fire and forget (e.g. restart Koolo)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (ExecRunner) Start(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	return cmd.Start()
}
