package monitor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func monitorSafetyConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Setenv("TB_NO_PROVIDER_CHECK", "true")
	if err := os.WriteFile(filepath.Join(root, "trueBlocks.toml"), []byte("[version]\ncurrent = \"v999.0.0\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	config.ReloadConfig()
	t.Cleanup(config.ReloadConfig)
}

func TestFreshenDuplicateAndOverlappingLocks(t *testing.T) {
	monitorSafetyConfig(t)
	a := "0xabcdef0123456789012345678901234567890123"
	b := "0x1234567890123456789012345678901234567890"
	done := make(chan error, 2)
	for _, addrs := range [][]string{{a, strings.ToUpper(a), b}, {b, a}} {
		go func(addrs []string) {
			u := NewUpdater("testchain", true, true, addrs)
			var monitors []Monitor
			_, err := u.FreshenMonitors(&monitors)
			for _, mon := range monitors {
				mon.Close()
			}
			if err == nil && len(u.Addrs) != 2 {
				done <- os.ErrInvalid
				return
			}
			done <- err
		}(addrs)
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("duplicate/overlapping wallet locks hung")
		}
	}
	// A later request must not inherit a lock retained by either batch.
	u := NewUpdater("testchain", true, true, []string{a})
	var monitors []Monitor
	if _, err := u.FreshenMonitors(&monitors); err != nil {
		t.Fatal(err)
	}
	for _, mon := range monitors {
		mon.Close()
	}
}

func TestAddressMutexConcurrentRegistry(t *testing.T) {
	const address = "registry-race-test"
	var wg sync.WaitGroup
	mutexes := make(chan *sync.Mutex, 100)
	for range 100 {
		wg.Add(1)
		go func() { defer wg.Done(); mutexes <- addressMutex(address) }()
	}
	wg.Wait()
	close(mutexes)
	first := <-mutexes
	for mutex := range mutexes {
		if mutex != first {
			t.Fatal("multiple locks for one address")
		}
	}
}

func TestFreshenRejectsStagingFailures(t *testing.T) {
	for _, mode := range []string{"copy", "header"} {
		t.Run(mode, func(t *testing.T) {
			monitorSafetyConfig(t)
			a := base.HexToAddress("0x1234567890123456789012345678901234567890")
			mon, err := NewMonitor("testchain", a, true)
			if err != nil {
				t.Fatal(err)
			}
			if err := mon.WriteMonHeader(false, 100, false); err != nil {
				t.Fatal(err)
			}
			if mode == "header" {
				if err := os.WriteFile(mon.Path(), []byte{1}, 0600); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(mon.Path())
			if err != nil {
				t.Fatal(err)
			}
			if mode == "copy" {
				staged := mon
				staged.Staged = true
				if err := os.Mkdir(staged.Path(), 0700); err != nil {
					t.Fatal(err)
				}
			}
			u := NewUpdater("testchain", true, false, []string{a.Hex()})
			var monitors []Monitor
			if _, err := u.FreshenMonitors(&monitors); err == nil {
				t.Fatal("staging failure was ignored")
			}
			after, err := os.ReadFile(mon.Path())
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("production changed: %v", err)
			}
		})
	}
}

func TestMoveToProductionReportsRenameFailure(t *testing.T) {
	monitorSafetyConfig(t)
	mon, err := NewMonitorStaged("testchain", "0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatal(err)
	}
	dest := PathToMonitorFile(mon.Chain, mon.Address)
	if err := os.Mkdir(dest, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "block-rename"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := mon.MoveToProduction(); err == nil {
		t.Fatal("rename failure ignored")
	}
	if !mon.Staged {
		t.Fatal("failed rename marked published")
	}
}

func TestFailedAppendDoesNotAdvanceCheckpoint(t *testing.T) {
	monitorSafetyConfig(t)
	mon, err := NewMonitorStaged("testchain", "0x1234567890123456789012345678901234567890")
	if err != nil {
		t.Fatal(err)
	}
	if err := mon.WriteMonHeader(false, 100, false); err != nil {
		t.Fatal(err)
	}
	// A directory cannot accept appended appearance records.
	if err := os.Remove(mon.Path()); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(mon.Path(), 0700); err != nil {
		t.Fatal(err)
	}
	apps := []types.AppRecord{{BlockNumber: 150, TransactionIndex: 1}}
	if err := mon.WriteAppearancesAppend(200, &apps); err == nil {
		t.Fatal("append failure ignored")
	}
	if mon.LastScanned != 100 {
		t.Fatalf("checkpoint advanced to %d", mon.LastScanned)
	}
}
