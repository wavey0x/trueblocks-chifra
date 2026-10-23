package rpc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/logger"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/prefunds"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc/query"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

type ClientKind string

const (
	ClientUnknown    ClientKind = "unknown"
	ClientReth       ClientKind = "reth"
	ClientErigon     ClientKind = "erigon"
	ClientGeth       ClientKind = "geth"
	ClientNethermind ClientKind = "nethermind"
)

type CapabilityCheck struct {
	Name   string
	OK     bool
	Method string
	Block  base.Blknum
	Detail string
	Err    error
}

func (check CapabilityCheck) Diagnostic() string {
	if check.Detail == "" {
		if check.Err != nil {
			return check.Err.Error()
		}
		return fmt.Sprintf("%s probe failed", check.Name)
	}
	if check.Err != nil && !strings.Contains(check.Detail, check.Err.Error()) {
		return check.Detail + ": " + check.Err.Error()
	}
	return check.Detail
}

type archiveProbe struct {
	Address     base.Address
	Block       base.Blknum
	Expected    base.Wei
	ExpectedHex string
	Context     string
	// Assume is set when the chain has no prefund data and no built-in probe. The node is
	// then assumed to be an archive node, as chifra did before the probe existed.
	Assume bool
}

const (
	mainnetArchiveProbeAddress = "0x5abfec25f74cd88437631a7731906932776356f9"
	mainnetArchiveProbeBalance = "0x9d83cc0dfa11177ff8000"
)

func DetectClientKind(version string) ClientKind {
	normalized := strings.ToLower(version)
	switch {
	case strings.Contains(normalized, "reth"):
		return ClientReth
	case strings.Contains(normalized, "erigon"):
		return ClientErigon
	case strings.Contains(normalized, "nethermind"):
		return ClientNethermind
	case strings.Contains(normalized, "geth"):
		return ClientGeth
	default:
		return ClientUnknown
	}
}

// IsNodeArchive tries to return true if the node is an archive
// node. Call CheckNodeArchive for the diagnostic detail behind the result.
func (conn *Connection) IsNodeArchive() bool {
	return conn.CheckNodeArchive().OK
}

func (conn *Connection) CheckNodeArchive() CapabilityCheck {
	check := CapabilityCheck{
		Name:   "archive",
		Method: "eth_getBalance",
	}

	probe, err := conn.getArchiveProbe()
	if err != nil {
		check.Detail = err.Error()
		check.Err = err
		return check
	}
	check.Block = probe.Block
	if probe.Assume {
		check.OK = true
		check.Detail = probe.Context
		logger.Warn(probe.Context)
		return check
	}

	probeCall := fmt.Sprintf("%s %s at block %d", check.Method, probe.Address.Hex(), probe.Block)
	result, err := query.Query[string](
		conn.Chain,
		check.Method,
		query.Params{probe.Address.Hex(), fmt.Sprintf("0x%x", probe.Block)},
	)
	if err != nil {
		check.Detail = withProbeContext(probeCall+" failed", probe.Context)
		check.Err = err
		return check
	}
	if result == nil {
		check.Detail = withProbeContext(probeCall+" returned no result", probe.Context)
		check.Err = errors.New("missing RPC result")
		return check
	}

	actual := base.NewWei(0)
	if _, ok := actual.SetString(*result, 0); !ok {
		check.Detail = withProbeContext(fmt.Sprintf("%s returned invalid balance %q", probeCall, *result), probe.Context)
		check.Err = errors.New("invalid RPC balance")
		return check
	}

	if actual.Cmp(&probe.Expected) != 0 {
		check.Detail = withProbeContext(
			fmt.Sprintf("%s returned %s, expected %s", probeCall, weiHex(actual), probe.ExpectedHex),
			probe.Context,
		)
		check.Err = errors.New("balance mismatch")
		return check
	}

	check.OK = true
	check.Detail = withProbeContext(
		fmt.Sprintf("%s returned expected balance %s", probeCall, probe.ExpectedHex),
		probe.Context,
	)
	return check
}

// IsNodeTracing returns true if the node exposes the `trace_block` RPC endpoint.
// It queries block 1 or a user supplied block (which we presume exists). The function
// returns false if trace_block returns an error or doesn't exist.
func (conn *Connection) IsNodeTracing() (error, bool) {
	check := conn.CheckNodeTracing()
	return check.Err, check.OK
}

func (conn *Connection) CheckNodeTracing() CapabilityCheck {
	firstTrace := max(1, base.KnownBlock(conn.Chain, base.FirstTrace))
	varName := "TB_" + strings.ToUpper(conn.Chain) + "_FIRSTTRACE"
	if len(os.Getenv(varName)) > 0 {
		firstTrace = max(firstTrace, base.MustParseValue(os.Getenv(varName)))
	}

	check := CapabilityCheck{
		Name:   "tracing",
		Method: "trace_block",
		Block:  firstTrace,
	}
	traces, err := query.Query[[]types.Trace](
		conn.Chain,
		check.Method,
		query.Params{fmt.Sprintf("0x%x", firstTrace)},
	)
	if err != nil {
		check.Detail = fmt.Sprintf("%s at block %d failed", check.Method, firstTrace)
		check.Err = err
		return check
	}
	if traces == nil || *traces == nil {
		check.Detail = fmt.Sprintf("%s at block %d returned no trace array", check.Method, firstTrace)
		check.Err = errors.New("missing trace_block result")
		return check
	}

	check.OK = true
	check.Detail = fmt.Sprintf("%s at block %d succeeded", check.Method, firstTrace)
	return check
}

func (conn *Connection) getArchiveProbe() (archiveProbe, error) {
	thePath := prefunds.GetPrefundPath(conn.Chain)
	largest, err := prefunds.GetLargestPrefund(conn.Chain, thePath)
	if err == nil {
		return archiveProbe{
			Address:     largest.Address,
			Block:       0,
			Expected:    largest.Balance,
			ExpectedHex: weiHex(&largest.Balance),
			Context:     "using largest prefund from " + thePath,
		}, nil
	}

	context := fmt.Sprintf("prefunds unavailable at %s (%v)", thePath, err)
	if conn.Chain == "mainnet" {
		return archiveProbe{
			Address:     base.HexToAddress(mainnetArchiveProbeAddress),
			Block:       0,
			Expected:    *base.NewWeiStr(mainnetArchiveProbeBalance),
			ExpectedHex: mainnetArchiveProbeBalance,
			Context:     context + "; using built-in mainnet genesis prefund probe",
		}, nil
	}

	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, prefunds.ErrNoPrefunds) {
		return archiveProbe{
			Assume:  true,
			Context: fmt.Sprintf("%s; no built-in archive probe for chain %s, assuming an archive node", context, conn.Chain),
		}, nil
	}
	return archiveProbe{}, fmt.Errorf("%s; no built-in archive probe for chain %s", context, conn.Chain)
}

func weiHex(w *base.Wei) string {
	if w == nil || w.IsZero() {
		return "0x0"
	}
	return "0x" + w.Text(16)
}

func withProbeContext(detail, context string) string {
	if context == "" {
		return detail
	}
	return detail + " (" + context + ")"
}
