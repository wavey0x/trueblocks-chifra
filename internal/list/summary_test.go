package listPkg

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/monitor"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/output"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func TestSummaryAPIIsIndependentOfAddressOrderAndDefaultPage(t *testing.T) {
	setupSummaryConfig(t)
	a := summaryMonitor(t, "0x1111111111111111111111111111111111111111", 300)
	b := summaryMonitor(t, "0x2222222222222222222222222222222222222222", 600)
	empty := summaryMonitor(t, "0x3333333333333333333333333333333333333333", 0)
	for _, bounds := range []bool{false, true} {
		for _, order := range [][]monitor.Monitor{{a, b}, {b, a}} {
			rows := runSummary(t, bounds, order, 0, base.NOPOSN, 0, 250, false)
			if len(rows) != 2 {
				t.Fatalf("got %d results: %v", len(rows), rows)
			}
			for _, row := range rows {
				want := 300
				if row["address"] == b.Address.Hex() {
					want = 600
				}
				key := "nRecords"
				if bounds {
					key = "count"
				}
				if row[key] != float64(want) {
					t.Fatalf("%s = %v, want %d", key, row[key], want)
				}
				if bounds && row["latestApp"].(map[string]any)["blockNumber"] != float64(want) {
					t.Fatalf("wrong latest appearance: %v", row)
				}
			}
		}
	}
	rows := runSummary(t, false, []monitor.Monitor{a, empty}, 0, base.NOPOSN, 0, 250, false)
	if rows[1]["nRecords"] != float64(0) {
		t.Fatalf("empty monitor: %v", rows[1])
	}
}

func TestFilteredSummariesResetPaginationForEachAddress(t *testing.T) {
	setupSummaryConfig(t)
	a := summaryMonitor(t, "0x1111111111111111111111111111111111111111", 300)
	b := summaryMonitor(t, "0x2222222222222222222222222222222222222222", 600)
	for _, bounds := range []bool{false, true} {
		rows := runSummary(t, bounds, []monitor.Monitor{a, b}, 100, 200, 10, 250, false)
		key := "nRecords"
		if bounds {
			key = "count"
		}
		for _, row := range rows {
			if row[key] != float64(91) {
				t.Fatalf("filtered summary: %v", rows)
			}
		}
		if len(rows) != 2 {
			t.Fatalf("missing filtered result: %v", rows)
		}
	}
	rows := runSummary(t, true, []monitor.Monitor{b}, 100, 200, 0, 20, true)
	row := rows[0]
	if row["count"] != float64(20) || row["firstApp"].(map[string]any)["blockNumber"] != float64(181) ||
		row["latestApp"].(map[string]any)["blockNumber"] != float64(200) {
		t.Fatalf("reversed bounds must remain chronological: %v", row)
	}
}

func setupSummaryConfig(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "config", "mainnet"), 0700); err != nil {
		t.Fatal(err)
	}
	text := fmt.Sprintf("[version]\ncurrent = \"v999.0.0\"\n[settings]\ncachePath = %q\nindexPath = %q\ndefaultChain = \"mainnet\"\n[chains.mainnet]\nchainId = \"1\"\nrpcProviders = [\"http://127.0.0.1:1\"]\n", filepath.Join(root, "cache"), filepath.Join(root, "unchained"))
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	config.ReloadConfig()
	config.GetRootConfig()
	t.Cleanup(config.ReloadConfig)
	f, err := os.Create(filepath.Join(config.PathToIndex("mainnet"), "ts.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for n := uint32(0); n <= 600; n++ {
		if err := binary.Write(f, binary.LittleEndian, [2]uint32{n, 1500000000 + n}); err != nil {
			t.Fatal(err)
		}
	}
}

func summaryMonitor(t *testing.T, address string, count int) monitor.Monitor {
	t.Helper()
	mon, err := monitor.NewMonitorStaged("mainnet", address)
	if err != nil {
		t.Fatal(err)
	}
	apps := make([]types.AppRecord, count)
	for n := range apps {
		apps[n] = types.AppRecord{BlockNumber: uint32(n + 1), TransactionIndex: 1}
	}
	if err := mon.WriteAppearancesAppend(1000, &apps); err != nil {
		t.Fatal(err)
	}
	mon.Close()
	return mon
}

func runSummary(t *testing.T, bounds bool, mons []monitor.Monitor, first, last base.Blknum, offset, limit uint64, reversed bool) []map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	writer := output.NewDefaultJsonWriter(recorder, false)
	opts := ListOptions{Count: !bounds, Bounds: bounds, FirstBlock: first, LastBlock: last, FirstRecord: offset, MaxRecords: limit, Reversed: reversed}
	opts.Globals.Chain = "mainnet"
	opts.Globals.Format = "json"
	opts.Globals.Writer = writer
	if !opts.Globals.IsApiMode() {
		t.Fatal("test must exercise API defaults")
	}
	var err error
	if bounds {
		err = opts.HandleBounds(output.NewRenderContext(), mons)
	} else {
		err = opts.HandleCount(output.NewRenderContext(), mons)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Data   []map[string]any `json:"data"`
		Errors []string         `json:"errors"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("%v: %s", err, recorder.Body.String())
	}
	if len(response.Errors) > 0 {
		t.Fatal(response.Errors)
	}
	return response.Data
}
