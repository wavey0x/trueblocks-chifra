package listPkg

import (
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/monitor"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/ranges"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

// readSummary uses the monitor's count and endpoints for unrestricted summaries.
// A filtered summary gets its own pagination counters for each address.
func (opts *ListOptions) readSummary(mon *monitor.Monitor) ([]types.Appearance, int, error) {
	defer mon.Close()
	if opts.FirstBlock == 0 && (opts.LastBlock == 0 || opts.LastBlock == base.NOPOSN) &&
		opts.FirstRecord == 0 && opts.GetMax() == base.NOPOS {
		count := int(mon.Count())
		if count == 0 || opts.Count {
			return nil, count, nil
		}
		var first, last types.AppRecord
		if err := mon.ReadAppearanceAt(1, &first); err != nil {
			return nil, 0, err
		}
		if err := mon.ReadAppearanceAt(int64(count), &last); err != nil {
			return nil, 0, err
		}
		return []types.Appearance{
			{Address: mon.Address, BlockNumber: first.BlockNumber, TransactionIndex: first.TransactionIndex},
			{Address: mon.Address, BlockNumber: last.BlockNumber, TransactionIndex: last.TransactionIndex},
		}, count, nil
	}
	lastBlock := opts.LastBlock
	if lastBlock == 0 {
		lastBlock = base.NOPOSN
	}
	filter := types.NewFilter(opts.Reversed, false, nil,
		ranges.BlockRange{First: opts.FirstBlock, Last: lastBlock},
		ranges.RecordRange{First: opts.FirstRecord, Last: opts.GetMax()})
	apps, count, err := mon.ReadAndFilterAppearances(filter, true)
	if opts.Reversed && len(apps) > 1 {
		// Bounds always name the chronological endpoints of the selected records.
		apps[0], apps[len(apps)-1] = apps[len(apps)-1], apps[0]
	}
	return apps, count, err
}
