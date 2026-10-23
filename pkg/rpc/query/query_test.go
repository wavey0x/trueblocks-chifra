package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
)

type batchTestRequest struct {
	Method string `json:"method"`
	ID     int    `json:"id"`
}

func TestQueryBatchMapsResponsesByID(t *testing.T) {
	ts := newBatchTestServer(t, func(reqs []batchTestRequest, r *http.Request) []map[string]any {
		requireBatchHeader(t, r, "X-Test-Header", "present")
		return []map[string]any{
			{"jsonrpc": "2.0", "id": reqs[1].ID, "result": "0x2"},
			{"jsonrpc": "2.0", "id": reqs[0].ID, "result": "0x1"},
		}
	})
	defer ts.Close()
	configureQueryTestChain(t, ts.URL)

	results, err := QueryBatchWithHeaders[string]("mainnet", map[string]string{"X-Test-Header": "present"}, []BatchPayload{
		{Key: "balance", Payload: &Payload{Method: "eth_getBalance", Params: Params{"0x1", "0x0"}}},
		{Key: "code", Payload: &Payload{Method: "eth_getCode", Params: Params{"0x1", "0x0"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if *results["balance"] != "0x1" {
		t.Fatalf("balance = %q", *results["balance"])
	}
	if *results["code"] != "0x2" {
		t.Fatalf("code = %q", *results["code"])
	}
}

func TestQueryBatchReturnsPerItemRPCError(t *testing.T) {
	ts := newBatchTestServer(t, func(reqs []batchTestRequest, r *http.Request) []map[string]any {
		return []map[string]any{
			{
				"jsonrpc": "2.0",
				"id":      reqs[0].ID,
				"error":   map[string]any{"code": -32000, "message": "missing trie node"},
			},
			{"jsonrpc": "2.0", "id": reqs[1].ID, "result": "0x"},
		}
	})
	defer ts.Close()
	configureQueryTestChain(t, ts.URL)

	_, err := QueryBatch[string]("mainnet", []BatchPayload{
		{Key: "balance", Payload: &Payload{Method: "eth_getBalance", Params: Params{"0x1", "0x0"}}},
		{Key: "code", Payload: &Payload{Method: "eth_getCode", Params: Params{"0x1", "0x0"}}},
	})
	if err == nil {
		t.Fatal("expected batch RPC error")
	}
	for _, want := range []string{"balance", "eth_getBalance", "-32000", "missing trie node"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got: %v", want, err)
		}
	}
}

func TestQueryBatchValidatesResponseLength(t *testing.T) {
	ts := newBatchTestServer(t, func(reqs []batchTestRequest, r *http.Request) []map[string]any {
		return []map[string]any{
			{"jsonrpc": "2.0", "id": reqs[0].ID, "result": "0x1"},
		}
	})
	defer ts.Close()
	configureQueryTestChain(t, ts.URL)

	_, err := QueryBatch[string]("mainnet", []BatchPayload{
		{Key: "balance", Payload: &Payload{Method: "eth_getBalance", Params: Params{"0x1", "0x0"}}},
		{Key: "code", Payload: &Payload{Method: "eth_getCode", Params: Params{"0x1", "0x0"}}},
	})
	if err == nil {
		t.Fatal("expected response count error")
	}
	if !strings.Contains(err.Error(), "response count 1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newBatchTestServer(t *testing.T, handler func([]batchTestRequest, *http.Request) []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reqs []batchTestRequest
		if err := json.NewDecoder(r.Body).Decode(&reqs); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "application/json")
		if err := json.NewEncoder(w).Encode(handler(reqs, r)); err != nil {
			t.Fatal(err)
		}
	}))
}

func requireBatchHeader(t *testing.T, r *http.Request, key, value string) {
	t.Helper()
	if got := r.Header.Get(key); got != value {
		t.Fatalf("header %s = %q, want %q", key, got, value)
	}
}

func configureQueryTestChain(t *testing.T, rpcURL string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config", "mainnet"), 0700); err != nil {
		t.Fatal(err)
	}
	configText := fmt.Sprintf(`
[version]
current = "v999.0.0"

[settings]
cachePath = "%[1]s"
indexPath = "%[2]s"
defaultChain = "mainnet"

[pinning]
gatewayUrl = ""
localPinUrl = "http://localhost:5001"
remotePinUrl = "https://api.pinata.cloud/pinning/pinFileToIPFS"

[unchained]
preferredPublisher = "publisher.unchainedindex.eth"
smartContract = "0x0c316b7042b419d07d343f2f4f5bd54ff731183d"

[chains.mainnet]
chain = "mainnet"
chainId = "1"
rpcProviders = ["%[3]s"]
symbol = "ETH"
blockTime = 12
`, filepath.Join(root, "cache"), filepath.Join(root, "unchained"), rpcURL)
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	config.ReloadConfig()
	t.Cleanup(config.ReloadConfig)
}
