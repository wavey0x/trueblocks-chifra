package rpc

import (
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc/query"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

type tokenDecimalsKey struct {
	Token base.Address
	Block base.Blknum
}

func validTokenWord(value string) bool {
	if !strings.HasPrefix(value, "0x") || len(value) != 66 {
		return false
	}
	_, err := hex.DecodeString(value[2:])
	return err == nil
}

// GetTokenDecimals reads numeric metadata at the same block as the quantity.
// The cache belongs to this connection/request, and never crosses block or chain boundaries.
func (conn *Connection) GetTokenDecimals(token base.Address, bn base.Blknum) (uint64, error) {
	if token.Equal(base.FAKE_ETH_ADDRESS) {
		return 18, nil
	}
	key := tokenDecimalsKey{token, bn}
	conn.decimalsMu.Lock()
	defer conn.decimalsMu.Unlock()
	if d, ok := conn.decimals[key]; ok {
		return d, nil
	}
	block := fmt.Sprintf("0x%x", bn)
	if bn == base.NOPOSN {
		block = "latest"
	}
	value, err := query.Query[string](conn.Chain, "eth_call", query.Params{map[string]any{"to": token.Hex(), "data": tokenStateDecimals}, block})
	if err != nil {
		return 0, err
	}
	if !validTokenWord(*value) {
		return 0, fmt.Errorf("%s decimals unavailable at %s", token.Hex(), block)
	}
	d, err := strconv.ParseUint(strings.TrimLeft((*value)[2:], "0"), 16, 8)
	if *value == "0x"+strings.Repeat("0", 64) {
		d, err = 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("%s invalid decimals at %s: %w", token.Hex(), block, err)
	}
	if conn.decimals == nil {
		conn.decimals = make(map[tokenDecimalsKey]uint64)
	}
	conn.decimals[key] = d
	return d, nil
}

// GetTokenBalanceState supplies numeric metadata before rendering, even on a warm balance cache.
func (conn *Connection) GetTokenBalanceState(token, holder base.Address, bn base.Blknum) (*types.Token, error) {
	balance, err := conn.GetBalanceAtToken(token, holder, bn)
	if err != nil {
		return nil, err
	}
	decimals, decErr := conn.GetTokenDecimals(token, bn)
	unavailable := false
	if decErr != nil {
		code, codeErr := conn.GetContractCodeAt(token, bn)
		if codeErr != nil {
			return nil, codeErr
		}
		if len(code) != 0 || balance.Sign() != 0 {
			return nil, decErr
		}
		// There is no token at this block: zero requires no historical metadata.
		decimals, decErr = conn.GetTokenDecimals(token, base.NOPOSN)
		unavailable = decErr != nil
	}
	return &types.Token{Address: token, Holder: holder, BlockNumber: bn, Balance: *balance,
		Decimals: decimals, DecimalsUnavailable: unavailable, TokenType: types.TokenErc20}, nil
}
