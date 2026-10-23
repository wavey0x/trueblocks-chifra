package prefunds

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
)

func TestLoadPrefundsMissingDoesNotCreateFile(t *testing.T) {
	thePath := filepath.Join(t.TempDir(), "allocs.csv")

	if _, err := LoadPrefunds("mainnet", thePath, nil); err == nil {
		t.Fatal("expected missing prefunds file to return an error")
	}

	if _, err := os.Stat(thePath); !os.IsNotExist(err) {
		t.Fatalf("LoadPrefunds created %s, stat err: %v", thePath, err)
	}
}

func TestLoadPrefundsEmptyFile(t *testing.T) {
	thePath := filepath.Join(t.TempDir(), "allocs.csv")
	if err := os.WriteFile(thePath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	allocs, err := LoadPrefunds("mainnet", thePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(allocs) != 0 {
		t.Fatalf("allocs length = %d, want 0", len(allocs))
	}
}

func TestLoadPrefundsCorruptCSVReturnsDiagnostic(t *testing.T) {
	thePath := filepath.Join(t.TempDir(), "allocs.csv")
	if err := os.WriteFile(thePath, []byte("address,balance\nnot-an-address,100\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPrefunds("mainnet", thePath, nil)
	if err == nil {
		t.Fatal("expected corrupt prefund CSV to fail")
	}
	if !strings.Contains(err.Error(), "invalid prefund address") {
		t.Fatalf("expected invalid address diagnostic, got: %v", err)
	}
}

func TestLoadPrefundsRejectsEnsNames(t *testing.T) {
	thePath := filepath.Join(t.TempDir(), "allocs.csv")
	if err := os.WriteFile(thePath, []byte("address,balance\nalice.eth,100\n"), 0600); err != nil {
		t.Fatal(err)
	}

	_, err := LoadPrefunds("mainnet", thePath, nil)
	if err == nil {
		t.Fatal("expected ENS prefund address to fail")
	}
	if !strings.Contains(err.Error(), "invalid prefund address") {
		t.Fatalf("expected invalid address diagnostic, got: %v", err)
	}
}

func TestGetLargestPrefund(t *testing.T) {
	thePath := filepath.Join(t.TempDir(), "allocs.csv")
	csv := strings.Join([]string{
		"address,balance",
		"0x1111111111111111111111111111111111111111,100",
		"0x2222222222222222222222222222222222222222,0x200",
		"0x3333333333333333333333333333333333333333,300",
		"",
	}, "\n")
	if err := os.WriteFile(thePath, []byte(csv), 0600); err != nil {
		t.Fatal(err)
	}

	largest, err := GetLargestPrefund("mainnet", thePath)
	if err != nil {
		t.Fatal(err)
	}

	if largest.Address != base.HexToAddress("0x2222222222222222222222222222222222222222") {
		t.Fatalf("largest address = %s", largest.Address.Hex())
	}
	if largest.Balance.Cmp(base.NewWeiStr("0x200")) != 0 {
		t.Fatalf("largest balance = %s", largest.Balance.String())
	}
}
