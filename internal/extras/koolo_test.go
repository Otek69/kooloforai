package extras

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func testKooloServer() *httptest.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "koolo", Version: "test"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_status"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"version":"v9","characters":{"Sorcaa":{"status":"In game","games":3}}}`}}}, nil, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "koolo_screenshot"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{Data: []byte{0xff, 0xd8}, MIMEType: "image/jpeg"}}}, nil, nil
	})
	return httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, nil))
}

func TestMCPKooloAgainstRealServer(t *testing.T) {
	ts := testKooloServer()
	k := &MCPKoolo{URL: ts.URL}
	ctx := context.Background()
	st, err := GetStatus(ctx, k)
	if err != nil || st.Version != "v9" || st.Characters["Sorcaa"].Games != 3 {
		t.Fatalf("status %+v err %v", st, err)
	}
	img, err := k.Image(ctx, "koolo_screenshot", nil)
	if err != nil || len(img) != 2 || img[0] != 0xff {
		t.Fatalf("image %v err %v", img, err)
	}

	// Koolo goes away (nothing listens on the address): calls fail with ErrKooloDown...
	good := k.URL
	k.drop()
	k.URL = "http://127.0.0.1:1/mcp"
	if _, err := k.Call(ctx, "koolo_status", nil); !errors.Is(err, ErrKooloDown) {
		t.Fatalf("want ErrKooloDown, got %v", err)
	}
	// ...and work again once it's back, without restarting the extras.
	k.URL = good
	if _, err := GetStatus(ctx, k); err != nil {
		t.Fatalf("did not reconnect: %v", err)
	}
	k.drop()
	ts.CloseClientConnections()
	ts.Listener.Close() // (httptest's Close would wait for the open MCP stream)
}
