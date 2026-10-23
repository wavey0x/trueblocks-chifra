//go:build darwin || linux

package monitor

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/file"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/index"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/ranges"
)

func TestFreshenWriteFailureKeepsPublishedMonitor(t *testing.T) {
	if os.Getenv("CHIFRA_TEST_WRITE_LIMIT") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestFreshenWriteFailureKeepsPublishedMonitor$")
		cmd.Env = append(os.Environ(), "CHIFRA_TEST_WRITE_LIMIT=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("write failure regression: %v\n%s", err, output)
		}
		return
	}
	monitorSafetyConfig(t)
	address := base.HexToAddress("0x1234567890123456789012345678901234567890")
	mon, err := NewMonitor("testchain", address, true)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(mon.Path())
	if err != nil {
		t.Fatal(err)
	}
	rng := ranges.FileRange{First: 0, Last: 99}
	path := filepath.Join(config.PathToIndex("testchain"), "finalized", rng.String()+".bin")
	hash := config.HeaderHash(config.ExpectedVersion())
	bloom := make([]byte, 42+index.BLOOM_WIDTH_IN_BYTES)
	binary.LittleEndian.PutUint16(bloom, file.SmallMagicNumber)
	copy(bloom[2:34], hash)
	binary.LittleEndian.PutUint32(bloom[34:], 1)
	for i := 42; i < len(bloom); i++ {
		bloom[i] = 0xff
	}
	if err := os.WriteFile(index.ToBloomPath(path), bloom, 0600); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, index.HeaderWidth+index.AddrRecordWidth+index.AppRecordWidth)
	binary.LittleEndian.PutUint32(data, file.MagicNumber)
	copy(data[4:36], hash)
	binary.LittleEndian.PutUint32(data[36:], 1)
	binary.LittleEndian.PutUint32(data[40:], 1)
	copy(data[44:64], address.Bytes())
	binary.LittleEndian.PutUint32(data[68:], 1)
	binary.LittleEndian.PutUint32(data[72:], 50)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var limit syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	restricted := limit
	restricted.Cur = 8
	signal.Ignore(syscall.SIGXFSZ)
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &restricted); err != nil {
		t.Fatal(err)
	}
	freshen := func() error {
		u := NewUpdater("testchain", true, false, []string{address.Hex()})
		var monitors []Monitor
		_, err := u.FreshenMonitors(&monitors)
		return err
	}
	err = freshen()
	if restoreErr := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	signal.Reset(syscall.SIGXFSZ)
	if err == nil {
		t.Fatal("failed append reported success")
	}
	after, err := os.ReadFile(mon.Path())
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed append changed production: %v", err)
	}
	if err := freshen(); err != nil {
		t.Fatal(err)
	}
	if err := mon.ReadMonitorHeader(); err != nil {
		t.Fatal(err)
	}
	defer mon.Close()
	if mon.LastScanned != 100 || mon.Count() != 1 {
		t.Fatalf("retry checkpoint=%d count=%d", mon.LastScanned, mon.Count())
	}
}
