package listPkg

import (
	"errors"
	"fmt"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/monitor"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/output"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/tslib"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

func (opts *ListOptions) HandleBounds(rCtx *output.RenderCtx, monitorArray []monitor.Monitor) error {
	chain := opts.Globals.Chain

	fetchData := func(modelChan chan types.Modeler, errorChan chan error) {
		if len(monitorArray) == 0 {
			errorChan <- errors.New("no monitors found in HandleBounds")
			return
		}

		emitted := 0
		missing := []string{}
		for _, mon := range monitorArray {
			if apps, cnt, err := opts.readSummary(&mon); err != nil {
				errorChan <- err
				return
			} else if cnt > 0 {
				firstTs, _ := tslib.FromBnToTs(chain, base.Blknum(apps[0].BlockNumber))
				latestTs, _ := tslib.FromBnToTs(chain, base.Blknum(apps[len(apps)-1].BlockNumber))
				s := types.Bounds{
					Count: uint64(cnt),
					FirstApp: types.Appearance{
						Address:          mon.Address,
						BlockNumber:      apps[0].BlockNumber,
						TransactionIndex: apps[0].TransactionIndex,
					},
					FirstTs: firstTs,
					LatestApp: types.Appearance{
						Address:          mon.Address,
						BlockNumber:      apps[len(apps)-1].BlockNumber,
						TransactionIndex: apps[len(apps)-1].TransactionIndex,
					},
					LatestTs: latestTs,
				}
				modelChan <- &s
				emitted++
			} else {
				missing = append(missing, mon.Address.Hex())
				continue
			}
		}
		if emitted == 0 {
			if len(missing) == 1 {
				errorChan <- fmt.Errorf("no appearances found for %s", missing[0])
			} else {
				errorChan <- fmt.Errorf("no appearances found for any requested address")
			}
		}
	}

	extraOpts := map[string]any{
		"appearances": true,
	}
	return output.StreamMany(rCtx, fetchData, opts.Globals.OutputOptsWithExtra(extraOpts))
}
