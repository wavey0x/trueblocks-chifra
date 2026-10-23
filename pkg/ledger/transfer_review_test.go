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
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/topics"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func reviewTransferFixture(t *testing.T) (*Reconciler, *types.Transaction, *int) {
	t.Helper()
	calls := new(int)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*calls++
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Method != "eth_call" {
			t.Errorf("unexpected RPC %s", req.Method)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": fmt.Sprintf("0x%064x", 6)})
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	cfg := fmt.Sprintf("[version]\ncurrent=\"v999.0.0\"\n[settings]\ncachePath=%q\nindexPath=%q\n[chains.mainnet]\nchainId=\"1\"\nrpcProviders=[%q]\n", filepath.Join(root, "cache"), filepath.Join(root, "index"), server.URL)
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	config.ReloadConfig()
	t.Cleanup(config.ReloadConfig)
	asset := base.HexToAddress("0x1111111111111111111111111111111111111111")
	holder := base.HexToAddress("0x2222222222222222222222222222222222222222")
	sender := base.HexToAddress("0x3333333333333333333333333333333333333333")
	log := types.Log{Address: asset, BlockNumber: 100, TransactionIndex: 1, Data: fmt.Sprintf("0x%064x", 123456789), Topics: []base.Hash{topics.TransferTopic, base.HexToHash(sender.Pad32()), base.HexToHash(holder.Pad32())}}
	tx := &types.Transaction{BlockNumber: 100, TransactionIndex: 1, Receipt: &types.Receipt{Status: 1, Logs: []types.Log{log}}}
	r := &Reconciler{Connection: rpc.TempConnection("mainnet"), Opts: &ReconcilerOptions{AccountFor: holder, AssetFilters: []base.Address{asset}, AppFilters: types.NewEmptyFilter(), AsEther: true}, Names: map[base.Address]types.Name{asset: {Address: asset, Decimals: 18}}, RemoveAirdrops: true}
	return r, tx, calls
}

func TestExportTransfersEtherUsesRPCDecimals(t *testing.T) {
	r, tx, calls := reviewTransferFixture(t)
	transfers, _, err := r.GetTransfers([]*types.Transaction{tx})
	if err != nil || len(transfers) != 1 {
		t.Fatalf("transfers=%v err=%v", transfers, err)
	}
	model := transfers[0].Model("mainnet", "json", false, map[string]any{"ether": true})
	if transfers[0].Decimals != 6 || model.Data["amountEth"] != "123.456789" || *calls != 1 {
		t.Fatalf("wrong amount: %v calls=%d", model.Data, *calls)
	}
}

func TestIgnoredAirdropDoesNotFetchMetadataOrBalances(t *testing.T) {
	r, tx, calls := reviewTransferFixture(t)
	asset := r.Opts.AssetFilters[0]
	r.Names[asset] = types.Name{Address: asset, Name: "spam airdrop"}
	node := types.NewAppNode(&types.NodeAppearance{BlockNumber: 100, TransactionIndex: 1}, tx)
	statements, err := r.GetStatements(node)
	if err != nil || len(statements) != 0 || *calls != 0 {
		t.Fatalf("ignored spam caused RPC: statements=%v calls=%d err=%v", statements, *calls, err)
	}
}
