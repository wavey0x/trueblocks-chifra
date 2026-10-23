package rpc

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func TestGetStateReturnsBatchRPCError(t *testing.T) {
	ts := newStateRPCServer(t, func(reqs []rpcTestRequest) []map[string]any {
		return []map[string]any{
			{
				"jsonrpc": "2.0",
				"id":      reqs[0].ID,
				"error":   map[string]any{"code": -32000, "message": "missing trie node"},
			},
		}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetState(types.Balance, base.HexToAddress("0x1111111111111111111111111111111111111111"), 100, StateFilters{})
	if err == nil {
		t.Fatal("expected GetState to return the batch RPC error")
	}
	for _, want := range []string{"balance", "eth_getBalance", "-32000", "missing trie node"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected error to contain %q, got: %v", want, err)
		}
	}
}

func TestGetStateRejectsInvalidBalanceResult(t *testing.T) {
	ts := newStateRPCServer(t, func(reqs []rpcTestRequest) []map[string]any {
		return []map[string]any{
			{"jsonrpc": "2.0", "id": reqs[0].ID, "result": "not-a-balance"},
		}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetState(types.Balance, base.HexToAddress("0x1111111111111111111111111111111111111111"), 100, StateFilters{})
	if err == nil {
		t.Fatal("expected invalid balance result to fail")
	}
	if !strings.Contains(err.Error(), "eth_getBalance returned invalid balance") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStateRejectsInvalidNonceResult(t *testing.T) {
	ts := newStateRPCServer(t, func(reqs []rpcTestRequest) []map[string]any {
		return []map[string]any{
			{"jsonrpc": "2.0", "id": reqs[0].ID, "result": "0x1"},
			{"jsonrpc": "2.0", "id": reqs[1].ID, "result": "not-a-nonce"},
		}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetState(types.Balance|types.Nonce, base.HexToAddress("0x1111111111111111111111111111111111111111"), 100, StateFilters{})
	if err == nil {
		t.Fatal("expected invalid nonce result to fail")
	}
	if !strings.Contains(err.Error(), "eth_getTransactionCount returned invalid value") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGetStateRejectsInvalidCodeResult(t *testing.T) {
	ts := newStateRPCServer(t, func(reqs []rpcTestRequest) []map[string]any {
		return []map[string]any{
			{"jsonrpc": "2.0", "id": reqs[0].ID, "result": "0x1"},
			{"jsonrpc": "2.0", "id": reqs[1].ID, "result": "not-code"},
		}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetState(types.Balance|types.Code, base.HexToAddress("0x1111111111111111111111111111111111111111"), 100, StateFilters{})
	if err == nil {
		t.Fatal("expected invalid code result to fail")
	}
	if !strings.Contains(err.Error(), "eth_getCode returned invalid code") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func newStateRPCServer(t *testing.T, batchHandler func([]rpcTestRequest) []map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}

		w.Header().Set("content-type", "application/json")
		if len(raw) > 0 && raw[0] == '[' {
			var reqs []rpcTestRequest
			if err := json.Unmarshal(raw, &reqs); err != nil {
				t.Fatal(err)
			}
			if err := json.NewEncoder(w).Encode(batchHandler(reqs)); err != nil {
				t.Fatal(err)
			}
			return
		}

		var req rpcTestRequest
		if err := json.Unmarshal(raw, &req); err != nil {
			t.Fatal(err)
		}
		if req.Method != "eth_getBlockByNumber" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result": map[string]any{
				"number":    "0x64",
				"timestamp": "0x1234",
			},
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatal(err)
		}
	}))
}
