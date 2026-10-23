package rpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc/query"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func decimalsRPCServer(t *testing.T, fn func(rpcTestRequest) any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Error(err)
			return
		}
		respond := func(req rpcTestRequest) map[string]any {
			value := fn(req)
			if e, ok := value.(map[string]any); ok && e["code"] != nil {
				return map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": e}
			}
			return map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": value}
		}
		if raw[0] == '[' {
			var reqs []rpcTestRequest
			_ = json.Unmarshal(raw, &reqs)
			out := []map[string]any{}
			for _, req := range reqs {
				out = append(out, respond(req))
			}
			_ = json.NewEncoder(w).Encode(out)
		} else {
			var req rpcTestRequest
			_ = json.Unmarshal(raw, &req)
			_ = json.NewEncoder(w).Encode(respond(req))
		}
	}))
}

func TestDecimalsCacheIsBlockScopedAndPreservesZero(t *testing.T) {
	calls := 0
	server := decimalsRPCServer(t, func(req rpcTestRequest) any {
		calls++
		var block string
		_ = json.Unmarshal(req.Params[1], &block)
		switch block {
		case "0x64":
			return fmt.Sprintf("0x%064x", 6)
		case "0x65":
			return fmt.Sprintf("0x%064x", 0)
		default:
			return fmt.Sprintf("0x%064x", 18)
		}
	})
	defer server.Close()
	conn, _ := configuredTestConnection(t, server.URL)
	token := base.HexToAddress("0x1111111111111111111111111111111111111111")
	for _, tt := range []struct {
		bn   base.Blknum
		want uint64
	}{{100, 6}, {100, 6}, {101, 0}, {102, 18}} {
		got, err := conn.GetTokenDecimals(token, tt.bn)
		if err != nil || got != tt.want {
			t.Fatalf("block %d decimals=%d err=%v", tt.bn, got, err)
		}
	}
	if calls != 3 {
		t.Fatalf("calls=%d want 3", calls)
	}
}

func TestPreDeploymentBalanceAndExistingContractEmptyResult(t *testing.T) {
	for _, mode := range []string{"undeployed", "no-metadata", "existing", "rpc-error"} {
		t.Run(mode, func(t *testing.T) {
			server := decimalsRPCServer(t, func(req rpcTestRequest) any {
				switch req.Method {
				case "eth_getBlockByNumber":
					return map[string]any{"number": "0x64", "timestamp": "0x1234"}
				case "eth_getCode":
					if mode == "existing" {
						return "0x6000"
					}
					return "0x"
				case "eth_call":
					var call map[string]any
					_ = json.Unmarshal(req.Params[0], &call)
					if mode == "rpc-error" {
						return map[string]any{"code": -32000, "message": "missing trie node"}
					}
					if call["data"] == tokenStateDecimals {
						var block string
						_ = json.Unmarshal(req.Params[1], &block)
						if block == "latest" && mode != "no-metadata" {
							return fmt.Sprintf("0x%064x", 6)
						}
					}
					return "0x"
				}
				t.Errorf("unexpected RPC %s", req.Method)
				return nil
			})
			defer server.Close()
			conn, _ := configuredTestConnection(t, server.URL)
			token := base.HexToAddress("0x1111111111111111111111111111111111111111")
			holder := base.HexToAddress("0x2222222222222222222222222222222222222222")
			got, err := conn.GetTokenBalanceState(token, holder, 100)
			if mode == "existing" || mode == "rpc-error" {
				if err == nil {
					t.Fatal("empty/error result became zero")
				}
				return
			}
			if err != nil || got.Balance.Sign() != 0 {
				t.Fatalf("pre-deployment: %v %v", got, err)
			}
			if mode == "undeployed" && (got.Decimals != 6 || got.DecimalsUnavailable) {
				t.Fatalf("latest label missing: %+v", got)
			}
			if mode == "no-metadata" && !got.DecimalsUnavailable {
				t.Fatal("unknown decimals fabricated")
			}
		})
	}
}

func TestMissingReceiptCannotLookLikeFailedTransaction(t *testing.T) {
	for _, result := range []any{nil, map[string]any{"status": "0x0"}} {
		server := decimalsRPCServer(t, func(req rpcTestRequest) any { return result })
		conn, _ := configuredTestConnection(t, server.URL)
		receipt, err := query.Query[types.Receipt](conn.Chain, "eth_getTransactionReceipt", query.Params{"0x" + strings.Repeat("1", 64)})
		server.Close()
		if result == nil {
			if err == nil || receipt != nil {
				t.Fatal("missing receipt fabricated")
			}
		} else if err != nil || receipt.Status != 0 {
			t.Fatalf("real failed receipt rejected: %v", err)
		}
	}
}

func TestNilCacheIsAMiss(t *testing.T) {
	conn := &Connection{Chain: "mainnet"}
	if err := conn.ReadFromCache(&types.Token{}); err == nil {
		t.Fatal("absent cache reported a hit")
	}
}

func TestMissingBlockAndTransactionRemainNotFound(t *testing.T) {
	server := decimalsRPCServer(t, func(req rpcTestRequest) any { return nil })
	defer server.Close()
	conn, _ := configuredTestConnection(t, server.URL)
	if _, err := conn.getBlockFromRpc(100, notAHash); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("missing block error: %v", err)
	}
	if _, err := conn.getTransactionFromRpc(notAHash, notAHash, 100, 1); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("missing transaction error: %v", err)
	}
}
