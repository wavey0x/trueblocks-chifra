package rpc

import (
	"strings"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

const (
	usdcNameResult   = "0x0000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000000855534420436f696e000000000000000000000000000000000000000000000000"
	usdcSymbolResult = "0x000000000000000000000000000000000000000000000000000000000000002000000000000000000000000000000000000000000000000000000000000000045553444300000000000000000000000000000000000000000000000000000000"
	usdcDecimals     = "0x0000000000000000000000000000000000000000000000000000000000000006"
	usdcTotalSupply  = "0x00000000000000000000000000000000000000000000000000aa7b8b43690fac"
)

var revertError = map[string]any{"code": 3, "message": "execution reverted"}

// tokenRPCServer answers the GetTokenState batch by key, in reverse order to exercise ID mapping.
func tokenRPCServer(t *testing.T, items map[string]map[string]any) func([]rpcTestRequest) []map[string]any {
	keys := []string{"name", "symbol", "decimals", "totalSupply", "erc721"}
	return func(reqs []rpcTestRequest) []map[string]any {
		if len(reqs) != len(keys) {
			t.Fatalf("expected %d batch items, got %d", len(keys), len(reqs))
		}
		out := make([]map[string]any, 0, len(reqs))
		for i := len(reqs) - 1; i >= 0; i-- {
			item := map[string]any{"jsonrpc": "2.0", "id": reqs[i].ID}
			for k, v := range items[keys[i]] {
				item[k] = v
			}
			out = append(out, item)
		}
		return out
	}
}

func TestGetTokenStateToleratesRevertedERC165Probe(t *testing.T) {
	ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
		"name":        {"result": usdcNameResult},
		"symbol":      {"result": usdcSymbolResult},
		"decimals":    {"result": usdcDecimals},
		"totalSupply": {"result": usdcTotalSupply},
		"erc721":      {"error": revertError},
	}))
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	token, err := conn.GetTokenState(base.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), "latest")
	if err != nil {
		t.Fatalf("plain ERC-20 rejected: %v", err)
	}
	if token.Name != "USD Coin" || token.Symbol != "USDC" || token.Decimals != 6 {
		t.Fatalf("wrong metadata: name=%q symbol=%q decimals=%d", token.Name, token.Symbol, token.Decimals)
	}
	if token.TotalSupply.String() != "47986584102440876" {
		t.Fatalf("wrong total supply: %s", token.TotalSupply.String())
	}
	if token.TokenType != types.TokenErc20 {
		t.Fatalf("token type = %v, want ERC-20", token.TokenType)
	}
}

func TestGetTokenStateTreatsRevertedOptionalFieldsAsMissing(t *testing.T) {
	ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
		"name":        {"error": revertError},
		"symbol":      {"result": usdcSymbolResult},
		"decimals":    {"result": usdcDecimals},
		"totalSupply": {"result": usdcTotalSupply},
		"erc721":      {"error": map[string]any{"code": -32000, "message": "execution reverted: unsupported"}},
	}))
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	token, err := conn.GetTokenState(base.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), "latest")
	if err != nil {
		t.Fatal(err)
	}
	if token.Name != "" || token.Symbol != "USDC" || token.Decimals != 6 {
		t.Fatalf("wrong metadata: %+v", token)
	}
}

func TestGetTokenStateReportsNonRevertErrors(t *testing.T) {
	ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
		"name":        {"result": usdcNameResult},
		"symbol":      {"result": usdcSymbolResult},
		"decimals":    {"error": map[string]any{"code": -32000, "message": "missing trie node"}},
		"totalSupply": {"result": usdcTotalSupply},
		"erc721":      {"error": revertError},
	}))
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetTokenState(base.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), "0x1")
	if err == nil || !strings.Contains(err.Error(), "missing trie node") {
		t.Fatalf("expected the node error, got %v", err)
	}
}

func TestGetTokenStateRejectsNonToken(t *testing.T) {
	ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
		"name":        {"error": revertError},
		"symbol":      {"error": revertError},
		"decimals":    {"error": revertError},
		"totalSupply": {"error": revertError},
		"erc721":      {"error": revertError},
	}))
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	_, err := conn.GetTokenState(base.HexToAddress("0x1111111111111111111111111111111111111111"), "latest")
	if err == nil || !strings.Contains(err.Error(), "address is not token") {
		t.Fatalf("expected not-a-token error, got %v", err)
	}
}

func TestGetTokenStateRejectsERC20WithoutTotalSupply(t *testing.T) {
	for _, missing := range []map[string]any{{"error": revertError}, {"result": "0x"}} {
		ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
			"name":        {"result": usdcNameResult},
			"symbol":      {"result": usdcSymbolResult},
			"decimals":    {"result": usdcDecimals},
			"totalSupply": missing,
			"erc721":      {"error": revertError},
		}))
		conn, _ := configuredTestConnection(t, ts.URL)
		_, err := conn.GetTokenState(base.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), "latest")
		ts.Close()
		if err == nil || !strings.Contains(err.Error(), "does not implement totalSupply") {
			t.Fatalf("totalSupply %v: expected an error, got %v", missing, err)
		}
	}
}

func TestGetTokenStateAllowsERC721WithoutTotalSupply(t *testing.T) {
	ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
		"name":        {"result": usdcNameResult},
		"symbol":      {"result": usdcSymbolResult},
		"decimals":    {"error": revertError},
		"totalSupply": {"error": revertError},
		"erc721":      {"result": "0x0000000000000000000000000000000000000000000000000000000000000001"},
	}))
	defer ts.Close()

	conn, _ := configuredTestConnection(t, ts.URL)
	token, err := conn.GetTokenState(base.HexToAddress("0x1111111111111111111111111111111111111111"), "latest")
	if err != nil {
		t.Fatal(err)
	}
	if token.TokenType != types.TokenErc721 || !token.TotalSupply.IsZero() || token.Decimals != 0 {
		t.Fatalf("unexpected ERC-721 token: %+v", token)
	}
}

func TestGetTokenStateRejectsUnknownERC20Decimals(t *testing.T) {
	for _, missing := range []map[string]any{{"error": revertError}, {"result": "0x"}} {
		ts := newStateRPCServer(t, tokenRPCServer(t, map[string]map[string]any{
			"name": {"result": usdcNameResult}, "symbol": {"result": usdcSymbolResult},
			"decimals": missing, "totalSupply": {"result": usdcTotalSupply}, "erc721": {"error": revertError},
		}))
		conn, _ := configuredTestConnection(t, ts.URL)
		_, err := conn.GetTokenState(base.HexToAddress("0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"), "latest")
		ts.Close()
		if err == nil || !strings.Contains(err.Error(), "decimals are unavailable") {
			t.Fatalf("unknown decimals became numeric data: %v", err)
		}
	}
}
