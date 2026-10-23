package exportPkg

import (
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/file"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/rpc"
)

func TestLogExportAcceptsSingleBlockAndRejectsReversedRange(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"jsonrpc":"2.0","id":1,"result":"0x100"}`)
	}))
	defer server.Close()
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("XDG_CACHE_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	text := fmt.Sprintf("[version]\ncurrent = \"v999.0.0\"\n[chains.mainnet]\nchainId = \"1\"\nrpcProviders = [%q]\n", server.URL)
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	config.ReloadConfig()
	t.Cleanup(config.ReloadConfig)
	config.GetRootConfig()
	header := make([]byte, 42)
	binary.LittleEndian.PutUint16(header, file.SmallMagicNumber)
	copy(header[2:34], config.HeaderHash(config.ExpectedVersion()))
	if err := os.WriteFile(filepath.Join(config.PathToIndex("mainnet"), "blooms", "000000000-000000000.bloom"), header, 0600); err != nil {
		t.Fatal(err)
	}
	opts := ExportOptions{Logs: true, Addrs: []string{"0x1111111111111111111111111111111111111111"}, FirstBlock: 100, LastBlock: 100}
	opts.Globals.Chain = "mainnet"
	opts.Globals.Format = "json"
	opts.Conn = &rpc.Connection{Chain: "mainnet"}
	if err := opts.validateExport(); err != nil {
		t.Fatalf("single block: %v", err)
	}
	opts.FirstBlock = 101
	if err := opts.validateExport(); err == nil {
		t.Fatal("reversed range accepted")
	}
}
