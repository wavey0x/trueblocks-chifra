package prefunds

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/gocarina/gocsv"
)

func GetPrefundPath(chain string) string {
	return filepath.Join(config.MustGetPathToChainConfig(chain), "allocs.csv")
}

// Allocation is a single allocation in the genesis file
type Allocation struct {
	Address base.Address `json:"address" csv:"address"`
	Balance base.Wei     `json:"balance" csv:"balance"`
}

type allocationRecord struct {
	Address string `csv:"address"`
	Balance string `csv:"balance"`
}

// ErrNoPrefunds means the prefund file holds no allocations (for example, it is empty).
var ErrNoPrefunds = errors.New("no valid prefunds found")

// emptyAllocs is a list of empty allocations. We use this to return at least one allocation
var emptyAllocs = []Allocation{}

type allocCallback func(*Allocation, *any) (bool, error)

// TODO: In the c++ code, the prefunds were cached in memory. We should do the same here.

// LoadPrefunds loads the prefunds from the genesis file and processes each with provided callback if present
func LoadPrefunds(chain string, thePath string, userCallback allocCallback) ([]Allocation, error) {
	_ = chain
	allocations := make([]Allocation, 0, 4000)
	row := 1 // header is row 1
	callbackFunc := func(record allocationRecord) error {
		row++
		if ok, err := base.ValidHex(record.Address, 20); !ok {
			if err != nil {
				return fmt.Errorf("invalid prefund address at row %d: %w", row, err)
			}
			return fmt.Errorf("invalid prefund address at row %d: %q", row, record.Address)
		}

		balance := base.NewWei(0)
		if _, ok := balance.SetString(record.Balance, 0); !ok {
			return fmt.Errorf("invalid prefund balance at row %d: %q", row, record.Balance)
		}

		alloc := Allocation{
			Address: base.HexToAddress(record.Address),
			Balance: *balance,
		}
		allocations = append(allocations, alloc)
		if userCallback != nil {
			if cont, err := userCallback(&alloc, nil); !cont || err != nil {
				return err
			}
		}
		return nil
	}

	if theFile, err := os.Open(thePath); err != nil {
		return emptyAllocs, fmt.Errorf("opening prefunds %s: %w", thePath, err)

	} else {
		defer theFile.Close()
		if info, err := theFile.Stat(); err == nil && info.Size() == 0 {
			return emptyAllocs, nil
		}
		if err := gocsv.UnmarshalToCallback(theFile, callbackFunc); err != nil {
			return emptyAllocs, fmt.Errorf("loading prefunds %s: %w", thePath, err)
		}
	}

	if len(allocations) == 0 {
		allocations = emptyAllocs
	}

	return allocations, nil
}

func GetLargestPrefund(chain, thePath string) (Allocation, error) {
	largest := Allocation{}
	getLargest := func(alloc *Allocation, data *any) (bool, error) {
		_ = data
		if alloc.Balance.Cmp(&largest.Balance) > 0 {
			largest = *alloc
		}
		return true, nil
	}

	_, err := LoadPrefunds(chain, thePath, getLargest)
	if err != nil {
		return Allocation{}, err
	}
	if largest.Address.IsZero() && largest.Balance.IsZero() {
		return Allocation{}, fmt.Errorf("%w in %s", ErrNoPrefunds, thePath)
	}

	return largest, nil
}
