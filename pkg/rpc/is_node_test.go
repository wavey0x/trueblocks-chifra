package rpc

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

type rpcTestRequest struct {
	Method string            `json:"method"`
	Params []json.RawMessage `json:"params"`
	ID     any               `json:"id"`
}

type rpcTestError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func TestDetectClientKind(t *testing.T) {
	tests := []struct {
		version string
		want    ClientKind
	}{
		{"reth/v2.2.0-88505c7/x86_64-unknown-linux-gnu", ClientReth},
		{"erigon/v2.60.0/linux-amd64/go1.22", ClientErigon},
		{"Geth/v1.13.15-stable/linux-amd64/go1.21", ClientGeth},
		{"Nethermind/v1.25.4/linux-x64/dotnet", ClientNethermind},
		{"RETH/V2.2.0", ClientReth},
		{"some-client/v1", ClientUnknown},
	}

	for _, test := range tests {
		t.Run(test.version, func(t *testing.T) {
			if got := DetectClientKind(test.version); got != test.want {
				t.Fatalf("DetectClientKind(%q) = %q, want %q", test.version, got, test.want)
			}
		})
	}
}

func TestCheckNodeArchiveMainnetFallbackMissingAllocs(t *testing.T) {
	var sawProbe bool
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		if req.Method != "eth_getBalance" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		requireJSONParam(t, req.Params, 0, mainnetArchiveProbeAddress)
		requireJSONParam(t, req.Params, 1, "0x0")
		sawProbe = true
		return mainnetArchiveProbeBalance, nil
	})
	defer ts.Close()

	conn, root := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeArchive()
	if !check.OK {
		t.Fatalf("archive check failed: %s", check.Diagnostic())
	}
	if !sawProbe {
		t.Fatal("archive probe was not sent")
	}
	if !strings.Contains(check.Detail, "built-in mainnet genesis prefund probe") {
		t.Fatalf("expected fallback detail, got: %s", check.Detail)
	}
	allocPath := filepath.Join(root, "config", "mainnet", "allocs.csv")
	if _, err := os.Stat(allocPath); !os.IsNotExist(err) {
		t.Fatalf("archive check created %s, stat err: %v", allocPath, err)
	}
}

func TestCheckNodeArchiveMainnetFallbackEmptyAllocs(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		requireJSONParam(t, req.Params, 0, mainnetArchiveProbeAddress)
		requireJSONParam(t, req.Params, 1, "0x0")
		return mainnetArchiveProbeBalance, nil
	})
	defer ts.Close()

	conn, root := configuredTestConnection(t, ts.URL)
	writeAllocs(t, root, "")

	check := conn.CheckNodeArchive()
	if !check.OK {
		t.Fatalf("archive check failed: %s", check.Diagnostic())
	}
	if !strings.Contains(check.Detail, "built-in mainnet genesis prefund probe") {
		t.Fatalf("expected fallback detail, got: %s", check.Detail)
	}
}

func TestCheckNodeArchiveMainnetFallbackCorruptAllocsKeepsDiagnostic(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		requireJSONParam(t, req.Params, 0, mainnetArchiveProbeAddress)
		requireJSONParam(t, req.Params, 1, "0x0")
		return mainnetArchiveProbeBalance, nil
	})
	defer ts.Close()

	conn, root := configuredTestConnection(t, ts.URL)
	writeAllocs(t, root, "address,balance\nalice.eth,100\n")

	check := conn.CheckNodeArchive()
	if !check.OK {
		t.Fatalf("archive check failed: %s", check.Diagnostic())
	}
	for _, want := range []string{"built-in mainnet genesis prefund probe", "invalid prefund address"} {
		if !strings.Contains(check.Detail, want) {
			t.Fatalf("expected detail to contain %q, got: %s", want, check.Detail)
		}
	}
}

func TestCheckNodeArchiveUsesValidLargestPrefund(t *testing.T) {
	const largestAddress = "0x2222222222222222222222222222222222222222"
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		requireJSONParam(t, req.Params, 0, largestAddress)
		requireJSONParam(t, req.Params, 1, "0x0")
		return "0x200", nil
	})
	defer ts.Close()

	conn, root := configuredTestConnection(t, ts.URL)
	writeAllocs(t, root, strings.Join([]string{
		"address,balance",
		"0x1111111111111111111111111111111111111111,100",
		largestAddress + ",0x200",
		"",
	}, "\n"))

	check := conn.CheckNodeArchive()
	if !check.OK {
		t.Fatalf("archive check failed: %s", check.Diagnostic())
	}
	if !strings.Contains(check.Detail, "using largest prefund") {
		t.Fatalf("expected largest prefund detail, got: %s", check.Detail)
	}
}

func TestCheckNodeArchiveHistoricalStateError(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		requireJSONParam(t, req.Params, 0, mainnetArchiveProbeAddress)
		requireJSONParam(t, req.Params, 1, "0x0")
		return nil, &rpcTestError{Code: -32000, Message: "missing trie node"}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeArchive()
	if check.OK {
		t.Fatal("expected archive check to fail")
	}
	if check.Method != "eth_getBalance" || check.Block != 0 {
		t.Fatalf("unexpected check metadata: %#v", check)
	}
	diag := check.Diagnostic()
	for _, want := range []string{"eth_getBalance", mainnetArchiveProbeAddress, "block 0", "missing trie node"} {
		if !strings.Contains(diag, want) {
			t.Fatalf("expected diagnostic to contain %q, got: %s", want, diag)
		}
	}
}

func TestCheckNodeArchiveBalanceMismatch(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		return "0x1", nil
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeArchive()
	if check.OK {
		t.Fatal("expected archive check to fail")
	}
	diag := check.Diagnostic()
	for _, want := range []string{"returned 0x1", "expected " + mainnetArchiveProbeBalance} {
		if !strings.Contains(diag, want) {
			t.Fatalf("expected diagnostic to contain %q, got: %s", want, diag)
		}
	}
}

func TestCheckNodeTracingSuccess(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		if req.Method != "trace_block" {
			t.Fatalf("unexpected method: %s", req.Method)
		}
		requireJSONParam(t, req.Params, 0, "0x1")
		return []any{}, nil
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeTracing()
	if !check.OK {
		t.Fatalf("tracing check failed: %s", check.Diagnostic())
	}
	if check.Method != "trace_block" || check.Block != 1 {
		t.Fatalf("unexpected check metadata: %#v", check)
	}
}

func TestCheckNodeTracingNullResult(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		return nil, nil
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeTracing()
	if check.OK {
		t.Fatal("expected tracing check to fail")
	}
	for _, want := range []string{"trace_block", "block 1", "returned no result"} {
		if !strings.Contains(check.Diagnostic(), want) {
			t.Fatalf("expected diagnostic to contain %q, got: %s", want, check.Diagnostic())
		}
	}
}

func TestCheckNodeTracingMissingResult(t *testing.T) {
	ts := newRPCServerMissingResult(t)
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeTracing()
	if check.OK {
		t.Fatal("expected tracing check to fail")
	}
	for _, want := range []string{"trace_block", "block 1", "returned no result"} {
		if !strings.Contains(check.Diagnostic(), want) {
			t.Fatalf("expected diagnostic to contain %q, got: %s", want, check.Diagnostic())
		}
	}
}

func TestCheckNodeTracingMissingNamespace(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		return nil, &rpcTestError{Code: -32601, Message: "method trace_block not found"}
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeTracing()
	if check.OK {
		t.Fatal("expected tracing check to fail")
	}
	diag := check.Diagnostic()
	for _, want := range []string{"trace_block", "block 1", "method trace_block not found"} {
		if !strings.Contains(diag, want) {
			t.Fatalf("expected diagnostic to contain %q, got: %s", want, diag)
		}
	}
}

func TestCheckNodeTracingFirstTraceOverride(t *testing.T) {
	t.Setenv("TB_MAINNET_FIRSTTRACE", "42")
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		requireJSONParam(t, req.Params, 0, "0x2a")
		return []any{}, nil
	})
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	check := conn.CheckNodeTracing()
	if !check.OK {
		t.Fatalf("tracing check failed: %s", check.Diagnostic())
	}
	if check.Block != 42 {
		t.Fatalf("check block = %d, want 42", check.Block)
	}
}

func newRPCServer(t *testing.T, handler func(rpcTestRequest) (any, *rpcTestError)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "application/json")
		result, rpcErr := handler(req)
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
		}
		if rpcErr != nil {
			response["error"] = rpcErr
		} else {
			response["result"] = result
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatal(err)
		}
	}))
}

func newRPCServerMissingResult(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req rpcTestRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("content-type", "application/json")
		response := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatal(err)
		}
	}))
}

func configuredTestConnection(t *testing.T, rpcURL string) (*Connection, string) {
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

	return TempConnection("mainnet"), root
}

func requireJSONParam(t *testing.T, params []json.RawMessage, index int, want string) {
	t.Helper()
	if len(params) <= index {
		t.Fatalf("missing param %d in %v", index, params)
	}
	var got string
	if err := json.Unmarshal(params[index], &got); err != nil {
		t.Fatalf("param %d is not a string: %v", index, err)
	}
	if !strings.EqualFold(got, want) {
		t.Fatalf("param %d = %q, want %q", index, got, want)
	}
}

func writeAllocs(t *testing.T, root string, content string) {
	t.Helper()
	allocPath := filepath.Join(root, "config", "mainnet", "allocs.csv")
	if err := os.WriteFile(allocPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func configureExtraChain(t *testing.T, root, chain, chainID, rpcURL string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "config", chain), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(root, "trueBlocks.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "\n[chains.%[1]s]\nchain = \"%[1]s\"\nchainId = \"%[2]s\"\nrpcProviders = [\"%[3]s\"]\nsymbol = \"ETH\"\nblockTime = 2\n", chain, chainID, rpcURL); err != nil {
		t.Fatal(err)
	}
	config.ReloadConfig()
}

func TestCheckNodeArchiveAssumesArchiveWithoutAllocsOnOtherChains(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		t.Fatalf("no probe should be sent, got %s", req.Method)
		return nil, nil
	})
	defer ts.Close()

	_, root := configuredTestConnection(t, ts.URL)
	configureExtraChain(t, root, "optimism", "10", ts.URL)
	check := TempConnection("optimism").CheckNodeArchive()
	if !check.OK || check.Err != nil {
		t.Fatalf("chain without allocs.csv must keep the archive assumption: %s", check.Diagnostic())
	}
	if !strings.Contains(check.Detail, "assuming an archive node") {
		t.Fatalf("expected the assumption in the detail, got: %s", check.Detail)
	}
}

func TestCheckNodeArchiveRejectsCorruptAllocsOnOtherChains(t *testing.T) {
	ts := newRPCServer(t, func(req rpcTestRequest) (any, *rpcTestError) {
		t.Fatalf("no probe should be sent, got %s", req.Method)
		return nil, nil
	})
	defer ts.Close()

	_, root := configuredTestConnection(t, ts.URL)
	configureExtraChain(t, root, "optimism", "10", ts.URL)
	if err := os.WriteFile(filepath.Join(root, "config", "optimism", "allocs.csv"), []byte("address,balance\nnot-an-address,0x1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	check := TempConnection("optimism").CheckNodeArchive()
	if check.OK {
		t.Fatal("corrupt allocs.csv must not be treated as proof of an archive node")
	}
}
