package types

import (
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"testing"
)

func TestTokenMetadataUsesRPCValuesWithoutNameCache(t *testing.T) {
	for _, test := range []struct {
		decimals  uint64
		raw       string
		formatted string
	}{
		{6, "123456789", "123.456789"},
		{0, "42", "42"},
	} {
		token := Token{Address: base.HexToAddress("0x1111111111111111111111111111111111111111"), Name: "Test Token", Symbol: "TEST", Decimals: test.decimals, TotalSupply: *base.NewWeiStr(test.raw)}
		model := token.Model("mainnet", "json", false, map[string]any{"parts": []string{"all"}, "loadNames": false})
		if model.Data["decimals"] != test.decimals || model.Data["name"] != "Test Token" || model.Data["symbol"] != "TEST" || model.Data["totalSupply"] != test.formatted {
			t.Fatalf("RPC metadata replaced or scaled incorrectly: %v", model.Data)
		}
	}
}

func TestTokenBalanceIgnoresWrongLabelDecimals(t *testing.T) {
	address := base.HexToAddress("0x1111111111111111111111111111111111111111")
	for _, d := range []uint64{0, 6, 18} {
		token := Token{Address: address, Decimals: d, Balance: *base.NewWeiStr("1000000")}
		model := token.Model("mainnet", "json", false, map[string]any{"parts": []string{"all_held"}, "namesMap": map[base.Address]Name{address: {Address: address, Decimals: 12}}})
		if model.Data["decimals"] != d || model.Data["balanceDec"] != token.Balance.ToFloatString(int(d)) {
			t.Fatalf("label changed quantity: %v", model.Data)
		}
	}
	token := Token{Address: address, DecimalsUnavailable: true}
	model := token.Model("mainnet", "json", false, map[string]any{"parts": []string{"all_held"}})
	if _, ok := model.Data["decimals"]; ok {
		t.Fatal("pre-deployment unknown decimals fabricated")
	}
}
