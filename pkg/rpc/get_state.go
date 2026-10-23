package rpc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/logger"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc/query"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/walk"
)

type StateFilters struct {
	BalanceCheck func(address base.Address, balance *base.Wei) bool
}

// GetState returns account state (search: FromRpc)
func (conn *Connection) GetState(fieldBits types.StatePart, address base.Address, blockNumber base.Blknum, filters StateFilters) (*types.State, error) {
	state := &types.State{
		BlockNumber: blockNumber,
		Address:     address,
	}
	if err := conn.ReadFromCache(state); err == nil {
		if state.Parts&fieldBits == fieldBits {
			if filters.BalanceCheck != nil {
				if !filters.BalanceCheck(address, &state.Balance) {
					return nil, nil
				}
				return state, nil
			}
		}
		fieldBits |= state.Parts // preserve what's there
	}

	// We always ask for balance even if we dont' need it. Not sure why.
	blockTs := conn.GetBlockTimestamp(blockNumber)
	rpcPayload := []query.BatchPayload{
		{
			Key: "balance",
			Payload: &query.Payload{
				Method: "eth_getBalance",
				Params: query.Params{
					address,
					fmt.Sprintf("0x%x", blockNumber),
				},
			},
		},
	}

	if (fieldBits & types.Nonce) != 0 {
		rpcPayload = append(rpcPayload, query.BatchPayload{
			Key: "nonce",
			Payload: &query.Payload{
				Method: "eth_getTransactionCount",
				Params: query.Params{
					address,
					fmt.Sprintf("0x%x", blockNumber),
				},
			},
		})
	}

	if (fieldBits & types.Code) != 0 {
		rpcPayload = append(rpcPayload, query.BatchPayload{
			Key: "code",
			Payload: &query.Payload{
				Method: "eth_getCode",
				Params: query.Params{
					address,
					fmt.Sprintf("0x%x", blockNumber),
				},
			},
		})
	}

	queryResults, err := query.QueryBatch[string](conn.Chain, rpcPayload)
	if err != nil {
		return nil, err
	}

	value, err := requiredRPCString(queryResults, "balance", "eth_getBalance")
	if err != nil {
		return nil, err
	}
	balance := base.NewWei(0)
	if _, ok := balance.SetString(value, 0); !ok {
		return nil, fmt.Errorf("eth_getBalance returned invalid balance %q", value)
	}

	state = &types.State{
		Address:     address,
		BlockNumber: blockNumber,
		Deployed:    base.NOPOSN,
		Timestamp:   blockTs,
		Parts:       fieldBits,
	}

	if (fieldBits & types.Balance) != 0 {
		state.Balance = *balance
	}

	if (fieldBits & types.Nonce) != 0 {
		if value, err := requiredRPCString(queryResults, "nonce", "eth_getTransactionCount"); err != nil {
			return nil, err
		} else {
			nonce, err := parseRPCValue("eth_getTransactionCount", value)
			if err != nil {
				return nil, err
			}
			state.Nonce = nonce
		}
	}

	if (fieldBits & types.Code) != 0 {
		code, err := requiredRPCString(queryResults, "code", "eth_getCode")
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(code, "0x") {
			return nil, fmt.Errorf("eth_getCode returned invalid code %q", code)
		}
		if code != "0x" {
			state.Code = code
		}
	}

	if (fieldBits & types.Deployed) != 0 {
		block, err := conn.GetContractDeployBlock(address)
		if err != nil && !errors.Is(err, ErrNotAContract) {
			return nil, err
		}
		// If err is ErrNotAContract, then we'll use the default value
		if err == nil {
			state.Deployed = block
		}
	}

	var proxy base.Address

	if (fieldBits&types.Proxy) != 0 || (fieldBits&types.Type) != 0 {
		proxy, err = conn.GetContractProxyAt(address, blockNumber)
		if err != nil {
			return nil, err
		}
		if (fieldBits & types.Proxy) != 0 {
			state.Proxy = proxy
		}
	}

	if (fieldBits & types.Type) != 0 {
		if !proxy.IsZero() {
			state.AccountType = "Proxy"
		} else {
			state.AccountType = conn.getTypeNonProxy(address, blockNumber)
		}
	}

	err = conn.WriteToCache(state, walk.Cache_State, blockTs)
	if filters.BalanceCheck != nil {
		if !filters.BalanceCheck(address, &state.Balance) {
			return nil, err
		}
	}
	return state, err
}

func requiredRPCString(results map[string]*string, key, method string) (string, error) {
	value, ok := results[key]
	if !ok || value == nil {
		return "", fmt.Errorf("%s returned no result", method)
	}
	return *value, nil
}

func parseRPCValue(method, value string) (base.Value, error) {
	parsed, err := strconv.ParseUint(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("%s returned invalid value %q: %w", method, value, err)
	}
	return base.Value(parsed), nil
}

// getBalanceAt returns a balance for an address at a block
func (conn *Connection) getBalanceAt(addr base.Address, bn base.Blknum) (*base.Wei, error) {
	state := &types.State{
		Address:     addr,
		BlockNumber: bn,
		Parts:       types.Balance,
	}
	if err := conn.ReadFromCache(state); err == nil {
		return &state.Balance, nil
	}

	var balance *base.Wei

	if ec, err := conn.getClient(); err != nil {
		var zero base.Wei
		return &zero, err
	} else {
		defer ec.Close()
		if ret, err := ec.BalanceAt(context.Background(), addr.Common(), base.BiFromBn(bn)); err != nil {
			return (*base.Wei)(ret), err
		} else {
			balance = (*base.Wei)(ret)
		}
	}

	// Write to disk cache using State type
	state.Balance = *balance
	state.Timestamp = conn.GetBlockTimestamp(bn)
	if err := conn.WriteToCache(state, walk.Cache_State, state.Timestamp); err != nil {
		logger.Warn("Failed to write ETH balance to cache:", err)
	}

	return balance, nil
}

func (conn *Connection) getTypeNonProxy(address base.Address, bn base.Blknum) string {
	isContractErr := conn.IsContractAt(address, bn)
	if errors.Is(isContractErr, ErrNotAContract) {
		return "EOA"
	}
	return "Contract"
}
