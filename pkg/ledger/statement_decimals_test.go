package ledger

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/walk"
)

func TestStatementDecimalsSurviveWarmCache(t *testing.T) {
	decimalCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     int               `json:"id"`
			Method string            `json:"method"`
			Params []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		var result any
		if req.Method == "eth_call" {
			decimalCalls++
			var block string
			_ = json.Unmarshal(req.Params[1], &block)
			if block != "0x64" {
				t.Errorf("wrong historical block %s", block)
			}
			result = fmt.Sprintf("0x%064x", 6)
		} else if req.Method == "eth_getBlockByNumber" {
			result = map[string]any{"number": "0x64", "timestamp": "0x1234"}
		} else {
			t.Errorf("unexpected method %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	defer server.Close()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	text := fmt.Sprintf("[version]\ncurrent = \"v999.0.0\"\n[settings]\ncachePath = %q\nindexPath = %q\n[chains.mainnet]\nchainId = \"1\"\nrpcProviders = [%q]\n", filepath.Join(root, "cache"), filepath.Join(root, "index"), server.URL)
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	config.ReloadConfig()
	t.Cleanup(config.ReloadConfig)
	conn := rpc.TestConnection("mainnet", true, map[walk.CacheType]bool{walk.Cache_Statements: true})
	asset := base.HexToAddress("0x1111111111111111111111111111111111111111")
	holder := base.HexToAddress("0x2222222222222222222222222222222222222222")
	r := &Reconciler{Connection: conn, Opts: &ReconcilerOptions{AccountFor: holder}, Names: map[base.Address]types.Name{asset: {Address: asset, Decimals: 18}}}
	trans := &types.Transaction{BlockNumber: 100, TransactionIndex: 1, Timestamp: 1234}
	stmt := types.Statement{Asset: asset, Holder: holder, BlockNumber: 100, TransactionIndex: 1, AmountIn: *base.NewWeiStr("123456789"), Decimals: 18}
	if err := r.resolveStatementDecimals(&stmt); err != nil {
		t.Fatal(err)
	}
	if err := r.statementsToCache(trans, []types.Statement{stmt}); err != nil {
		t.Fatal(err)
	}
	warm, ok := r.statementsFromCache(trans)
	if !ok || len(warm) != 1 || warm[0].Decimals != 6 {
		t.Fatalf("warm statement: %+v hit=%v", warm, ok)
	}
	model := warm[0].Model("mainnet", "json", false, map[string]any{"ether": true, "loadNames": false})
	if model.Data["amountInEth"] != "123.456789" {
		t.Fatalf("wrong scaled amount: %v", model.Data["amountInEth"])
	}
	if decimalCalls != 1 {
		t.Fatalf("warm cache fetched metadata again: calls=%d", decimalCalls)
	}
}
