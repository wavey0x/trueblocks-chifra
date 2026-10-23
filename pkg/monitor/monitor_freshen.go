package monitor

// Copyright 2021 The TrueBlocks Authors. All rights reserved.
// Use of this source code is governed by a license that can
// be found in the LICENSE file.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/base"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/colors"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/config"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/file"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/index"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/logger"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/manifest"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/ranges"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/sigintTrap"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/validate"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/walk"
)

// MonitorUpdate stores the original 'chifra list' command line options plus
type MonitorUpdate struct {
	MaxTasks      int
	MonitorMap    map[base.Address]*Monitor
	Chain         string
	PublisherAddr base.Address
	TestMode      bool
	SkipFreshen   bool
	FirstBlock    base.Blknum
	Addrs         []string
}

func NewUpdater(chain string, testMode, skipFreshen bool, addrs []string) MonitorUpdate {
	return MonitorUpdate{
		MaxTasks:      12,
		FirstBlock:    base.NOPOSN,
		Chain:         chain,
		PublisherAddr: base.Address{},
		TestMode:      testMode,
		SkipFreshen:   skipFreshen,
		Addrs:         addrs,
		MonitorMap:    make(map[base.Address]*Monitor, len(addrs)),
	}
}

const maxTestingBlock = 17000000

// mutexesPerAddress map stores mutex for each address that is being/has been freshened
var mutexesPerAddress = make(map[string]*sync.Mutex)
var mutexesPerAddressMutex sync.Mutex

// addressMutex returns the one mutex registered for a canonical address.
func addressMutex(address string) *sync.Mutex {
	mutexesPerAddressMutex.Lock()
	defer mutexesPerAddressMutex.Unlock()
	if mutex := mutexesPerAddress[address]; mutex != nil {
		return mutex
	}
	mutex := &sync.Mutex{}
	mutexesPerAddress[address] = mutex
	return mutex
}

func lockForAddress(address string)   { addressMutex(address).Lock() }
func unlockForAddress(address string) { addressMutex(address).Unlock() }

func (updater *MonitorUpdate) FreshenMonitors(monitorArray *[]Monitor) (bool, error) {

	// TODO: There are special case addresses for the sender of mining rewards and
	// TODO: prefund allocations that get ignored here because they are baddresses.
	// TODO: We could, if we wished, create special cases here to (for example) report
	// TODO: the balance sheet for "Ethereum" as a whole. How much has been spent on mining?
	// TODO: How much was the original allocation worth?
	// Parsing can resolve different spellings or ENS names to the same wallet.
	// Deduplicate here as well, before taking any non-reentrant locks.
	seen := make(map[string]bool, len(updater.Addrs))
	addrs := make([]string, 0, len(updater.Addrs))
	for _, address := range updater.Addrs {
		canonical := base.HexToAddress(address)
		address = canonical.Hex()
		if !seen[address] {
			seen[address] = true
			addrs = append(addrs, address)
		}
	}
	updater.Addrs = addrs
	lockOrder := append([]string(nil), addrs...)
	sort.Strings(lockOrder)
	for _, address := range lockOrder {
		lockForAddress(address)
		defer unlockForAddress(address)
	}
	// A failed refresh must never leave its partial staging copy publishable.
	discardStaged := true
	defer func() {
		if discardStaged {
			for _, mon := range updater.MonitorMap {
				if mon.Staged {
					mon.Close()
					_ = os.Remove(mon.Path())
				}
			}
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cleanOnQuit := func() {
		logger.Warn(colors.Yellow+"User hit control+c...", colors.Off)
	}
	trapChannel := sigintTrap.Enable(ctx, cancel, cleanOnQuit)
	defer sigintTrap.Disable(trapChannel)

	// This removes dups and keeps a map from address to a pointer to the monitors
	for _, addr := range updater.Addrs {
		err := needsMigration(addr)
		if err != nil {
			return ctx.Err() != nil, err
		}

		if updater.MonitorMap[base.HexToAddress(addr)] == nil {
			mon, err := NewMonitorStaged(updater.Chain, addr)
			if err != nil {
				return false, fmt.Errorf("stage %s: %w", addr, err)
			}
			if err := mon.ReadMonitorHeader(); err != nil {
				mon.Close()
				_ = os.Remove(mon.Path())
				return false, fmt.Errorf("read staged header %s: %w", addr, err)
			}
			bn := base.Blknum(mon.LastScanned)
			if bn < updater.FirstBlock {
				updater.FirstBlock = bn
			}
			*monitorArray = append(*monitorArray, mon)
			// we need the address here because we want to modify this object below
			updater.MonitorMap[mon.Address] = &(*monitorArray)[len(*monitorArray)-1]
		}
	}

	if updater.SkipFreshen {
		discardStaged = false // The caller reads this unchanged staged snapshot.
		return ctx.Err() != nil, nil
	}

	bloomPath := filepath.Join(config.PathToIndex(updater.Chain), "blooms")
	files, err := os.ReadDir(bloomPath)
	if err != nil {
		return ctx.Err() != nil, err
	}

	jobs := make([]freshenJob, 0, len(files))
	for _, info := range files {
		if ctx.Err() != nil {
			logger.Warn(colors.Yellow+"Finishing current tasks...", colors.Off)
			break
		}
		if info.IsDir() {
			continue
		}
		fileName := filepath.Join(bloomPath, info.Name())
		if !walk.IsCacheType(fileName, walk.Index_Bloom, true /* checkExt */) {
			continue // sometimes there are .gz files in this folder, for example
		}
		fileRange, err := ranges.RangeFromFilenameE(fileName)
		if err != nil {
			// don't respond further -- there may be foreign files in the folder
			fmt.Println(err)
			continue
		}

		max := os.Getenv("FAKE_FINAL_BLOCK") // This is for testing only, please ignore
		if len(max) > 0 {
			if fileRange.Last > base.MustParseBlknum(max) {
				continue
			}
		}

		if updater.TestMode && fileRange.Last > maxTestingBlock {
			continue
		}

		if fileRange.EarlierThanB(updater.FirstBlock) {
			continue
		}

		jobs = append(jobs, freshenJob{fileName: fileName, rng: fileRange})
	}

	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].rng.First != jobs[j].rng.First {
			return jobs[i].rng.First < jobs[j].rng.First
		}
		return jobs[i].rng.Last < jobs[j].rng.Last
	})

	jobs, holeErr := updater.stopAtMissingBloom(jobs)

	batchSize := updater.MaxTasks
	var firstErr error
	for start := 0; start < len(jobs) && ctx.Err() == nil && firstErr == nil; start += batchSize {
		end := start + batchSize
		if end > len(jobs) {
			end = len(jobs)
		}
		batch := jobs[start:end]
		var wg sync.WaitGroup
		resultChannel := make(chan []index.AppearanceResult, len(batch))
		for _, job := range batch {
			wg.Add(1)
			go updater.visitChunkToFreshenFinal(job.fileName, resultChannel, &wg)
		}
		wg.Wait()
		close(resultChannel)
		pending := make([]index.AppearanceResult, 0, len(batch)*len(updater.MonitorMap))
		for resArray := range resultChannel {
			pending = append(pending, resArray...)
		}
		keep, err := partitionFreshenResults(pending)
		for i := range keep {
			if writeErr := updater.updateMonitors(&keep[i]); writeErr != nil {
				return ctx.Err() != nil, writeErr
			}
		}
		firstErr = err
	}
	if firstErr == nil && ctx.Err() == nil {
		firstErr = holeErr
	}

	if firstErr == nil && ctx.Err() == nil && !updater.TestMode {
		// TODO: Note we could actually test this if we had the concept of a FAKE_HEAD block
		stagePath := index.ToStagingPath(filepath.Join(config.PathToIndex(updater.Chain), "staging"))
		stageFn, _ := file.LatestFileInFolder(stagePath)
		rng := ranges.RangeFromFilename(stageFn)
		lines := []string{}
		for addr, mon := range updater.MonitorMap {
			bn := base.Blknum(mon.LastScanned)
			if !rng.LaterThanB(bn) { // the range precedes the block number
				if len(lines) == 0 {
					lines = file.AsciiFileToLines(stageFn)
					sort.Slice(lines, func(i, j int) bool {
						return lines[i] < lines[j]
					})
				}

				addrStr := addr.Hex()
				found := sort.Search(len(lines), func(i int) bool {
					return lines[i] >= addrStr
				})
				if found < len(lines) && strings.HasPrefix(lines[found], addrStr) {
					apps := getAppearances(addrStr, lines, mon.LastScanned, found)
					if apps != nil {
						stageResult := index.AppearanceResult{
							Address:    addr,
							Range:      rng,
							AppRecords: apps,
						}
						if err := updater.updateMonitors(&stageResult); err != nil {
							return ctx.Err() != nil, err
						}
					}
				}
			}
		}
	}

	// A chunk interrupted by control+c still stops progress at its range (above), but it
	// is a cancellation, not a failure.
	canceled := ctx.Err() != nil || errors.Is(firstErr, index.ErrUserHitControlC)
	if errors.Is(firstErr, index.ErrUserHitControlC) {
		firstErr = nil
	}

	moveErr := updater.moveAllToProduction()
	if firstErr != nil {
		return canceled, firstErr
	}
	return canceled, moveErr
}

type freshenJob struct {
	fileName string
	rng      ranges.FileRange
}

// stopAtMissingBloom drops the (sorted) jobs from the first published chunk that has no bloom
// on disc. Freshen only sees blooms that exist, so a failed download would otherwise let
// LastScanned skip that range. Gaps between locally scraped chunks can be legitimate, so
// only the local manifest decides what should be present.
func (updater *MonitorUpdate) stopAtMissingBloom(jobs []freshenJob) ([]freshenJob, error) {
	if len(jobs) == 0 || !file.FileExists(config.PathToManifestFile(updater.Chain)) {
		return jobs, nil
	}
	man, err := manifest.LoadManifest(updater.Chain, updater.PublisherAddr, manifest.LocalCache)
	if err != nil {
		return nil, err
	}

	// Merge the local blooms into continuous spans. Overlapping and nested stale blooms still
	// count as coverage, but a published range is only present if one span covers all of it.
	spans := make([]ranges.FileRange, 0, len(jobs))
	for _, job := range jobs {
		if n := len(spans); n > 0 && job.rng.First <= spans[n-1].Last+1 {
			spans[n-1].Last = max(spans[n-1].Last, job.rng.Last)
		} else {
			spans = append(spans, job.rng)
		}
	}
	tip := spans[len(spans)-1].Last // spans are disjoint and ascending

	missing := ranges.FileRange{First: base.NOPOSN, Last: base.NOPOSN}
	for _, chunk := range man.Chunks {
		rng := ranges.RangeFromRangeString(chunk.Range)
		// The parser is lenient; a misread range could hide a hole, so require a round trip.
		if rng.String() != chunk.Range || rng.First > rng.Last {
			return nil, fmt.Errorf("malformed manifest range %q", chunk.Range)
		}
		if rng.EarlierThanB(updater.FirstBlock) || rng.First > tip || rng.First >= missing.First {
			continue
		}
		// Only the part of the range this freshen could scan must be covered: nothing before
		// FirstBlock, and nothing past the local tip (later blocks are not scanned yet).
		lo, hi := max(rng.First, updater.FirstBlock), min(rng.Last, tip)
		i := sort.Search(len(spans), func(i int) bool { return spans[i].Last >= lo })
		if i == len(spans) || spans[i].First > lo || spans[i].Last < hi {
			missing = rng
		}
	}
	if missing.First == base.NOPOSN {
		return jobs, nil
	}

	n := sort.Search(len(jobs), func(i int) bool { return jobs[i].rng.First > missing.First })
	return jobs[:n], fmt.Errorf("missing bloom filter for published chunk %s", missing)
}

// visitChunkToFreshenFinal opens an index file, searches for the address(es) we're looking for and pushes
// the appearance records down the resultsChannel (even if there are none).
func (updater *MonitorUpdate) visitChunkToFreshenFinal(fileName string, resultChannel chan<- []index.AppearanceResult, wg *sync.WaitGroup) {
	results := make([]index.AppearanceResult, 0, len(updater.MonitorMap))
	defer func() {
		resultChannel <- results
		wg.Done()
	}()

	bloomFilename := index.ToBloomPath(fileName)

	// We open the bloom filter and read its header but we do not read any of the
	// actual bits in the blooms. The IsMember function reads individual bytes to
	// check individual bits.
	bl, err := index.OpenBloom(bloomFilename, true /* check */)
	if err != nil {
		results = append(results, index.AppearanceResult{Range: bl.Range, Err: err})
		bl.Close()
		return
	}

	// We check each address against the bloom to see if there are any hits...
	var bloomHits = false
	for _, mon := range updater.MonitorMap {
		if bl.IsMember(mon.Address) {
			bloomHits = true
			break
		}
	}

	// We're done with the bloom and we want to close it as soon as we can (therefore
	// we don't defer this close, but close it right away -- otherwise too many files
	// are open and we get an error).
	// TODO: Must we always be closing these files?
	bl.Close()

	// If none of the addresses hit, we're finished with this index chunk. We want the
	// caller to note this range even though there was no hit. In this way, we keep
	// track of the last index portion we've seen. Because none of the addresses hit,
	// we don't need to send a specific message.
	if !bloomHits {
		results = append(results, index.AppearanceResult{Range: bl.Range})
		return
	}

	indexFilename := index.ToIndexPath(fileName)
	if !file.FileExists(indexFilename) {
		chain := updater.Chain
		var man *manifest.Manifest
		man, err = manifest.LoadManifest(chain, updater.PublisherAddr, manifest.LocalCache)
		if err != nil {
			results = append(results, index.AppearanceResult{Range: bl.Range, Err: err})
			return
		}

		err = index.DownloadOneChunk(chain, man, bl.Range)
		if err != nil {
			results = append(results, index.AppearanceResult{Range: bl.Range, Err: err})
			return
		}
	}

	indexChunk, err := index.OpenIndex(indexFilename, true /* check */)
	if err != nil {
		if errors.Is(err, index.ErrCorruptIndex) {
			updater.quarantineCorruptIndex(indexFilename, bl.Range)
		}
		results = append(results, index.AppearanceResult{Range: bl.Range, Err: err})
		return
	}

	defer indexChunk.Close()

	for _, mon := range updater.MonitorMap {
		results = append(results, *indexChunk.ReadAppearances(mon.Address))
	}
}

// quarantineCorruptIndex moves a corrupt index aside so a later run downloads it again.
// Chunks the manifest cannot restore (for example, ones scraped locally) stay in place.
func (updater *MonitorUpdate) quarantineCorruptIndex(indexFilename string, rng ranges.FileRange) {
	// LoadManifest falls back to the network when there is no local copy; stay local here.
	if !file.FileExists(config.PathToManifestFile(updater.Chain)) {
		logger.Warn("not quarantining corrupt index, no local manifest:", indexFilename)
		return
	}
	man, err := manifest.LoadManifest(updater.Chain, updater.PublisherAddr, manifest.LocalCache)
	if err != nil {
		logger.Warn("not quarantining corrupt index, manifest unavailable:", indexFilename, err)
		return
	}
	if pin := man.ChunkMap[rng.String()]; pin == nil || pin.IndexHash == "" {
		logger.Warn("not quarantining corrupt index missing from manifest:", indexFilename)
		return
	}
	if err := os.Rename(indexFilename, indexFilename+".corrupt"); err != nil && !os.IsNotExist(err) {
		logger.Error("failed to quarantine corrupt index", indexFilename, err)
	}
}

// partitionFreshenResults sorts chunk results and retains only ranges strictly
// before the earliest failed range so LastScanned cannot leapfrog a hole.
func partitionFreshenResults(results []index.AppearanceResult) ([]index.AppearanceResult, error) {
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Range.First != results[j].Range.First {
			return results[i].Range.First < results[j].Range.First
		}
		return results[i].Range.Last < results[j].Range.Last
	})

	var firstErr error
	holeFirst := base.NOPOSN
	for _, r := range results {
		if r.Err != nil {
			chunkErr := fmt.Errorf("%s: %w", r.Range, r.Err)
			if !errors.Is(r.Err, index.ErrUserHitControlC) {
				logger.Error("Error processing index file:", chunkErr)
			}
			if firstErr == nil || r.Range.First < holeFirst {
				firstErr = chunkErr
				holeFirst = r.Range.First
			}
		}
	}
	if firstErr == nil {
		return results, nil
	}
	n := 0
	for n < len(results) && results[n].Range.First < holeFirst {
		n++
	}
	return results[:n], firstErr
}

// updateMonitors writes an array of appearances to the Monitor file updating the header for lastScanned. It
// is called by 'chifra list' and 'chifra export' prior to reporting results
func (updater *MonitorUpdate) updateMonitors(result *index.AppearanceResult) error {
	if result == nil {
		return errors.New("cannot update monitor from a nil result")
	}

	if result.Err != nil {
		return fmt.Errorf("index range %s: %w", result.Range, result.Err)
	}

	lastScanned := uint32(result.Range.Last) + 1

	mon := updater.MonitorMap[result.Address]
	if mon == nil {
		// In this case, there were no errors, but all monitors were skipped. This would
		// happen, for example, due to false positives hits on the bloom filters. We want
		// to update the LastScanned values for each of these monitors. WriteAppearancesAppend, when
		// given a nil appearance list simply updates the header. Note we update for the
		// start of the next chunk (plus 1 to current range).
		for _, mon := range updater.MonitorMap {
			// TODO: Can we manage these file pointers so they aren't copied and
			// TODO: therefore don't need to be Closed so or else it crashes
			mon.Close()
			err := mon.WriteAppearancesAppend(lastScanned, nil)
			if err != nil {
				return fmt.Errorf("update %s: %w", mon.Address.Hex(), err)
			}
		}

	} else {
		mon.Close()
		// A chunk can start before this monitor's LastScanned (another monitor in the batch
		// is further behind, or local and published chunk boundaries differ). Appearances
		// before LastScanned are already in the file, so keep only the new ones.
		var apps []types.AppRecord
		if result.AppRecords != nil {
			for _, app := range *result.AppRecords {
				if app.BlockNumber >= mon.LastScanned {
					apps = append(apps, app)
				}
			}
		}
		// Note that apps may be empty here (because, for example, this monitor had
		// a false positive), but we do still want to update the header.
		if err := mon.WriteAppearancesAppend(lastScanned, &apps); err != nil {
			return fmt.Errorf("update %s: %w", mon.Address.Hex(), err)
		}
		if nWritten := len(apps); nWritten > 0 {
			msg := fmt.Sprintf("%s%s appended %5d apps at %s%s", colors.Green, mon.Address.Hex(), nWritten, result.Range, colors.Off)
			logger.Progress(!updater.TestMode, msg)
		}
	}
	return nil
}

func needsMigration(addr string) error {
	mon := Monitor{Address: base.HexToAddress(addr)}
	path := strings.ReplaceAll(mon.Path(), ".mon.bin", ".acct.bin")
	if file.FileExists(path) {
		path = filepath.Clean(strings.ReplaceAll(path, config.PathToCache(mon.Chain), "./"))
		return validate.Usage("Old style monitor found at {0}. Please run '{1}'", path, "chifra config --migrate cache")
	}
	return nil
}

// TODO: Somewhat iffy code here
const (
	startOfBlockNum = 42 + 1
	endOfBlockNum   = 42 + 1 + 9
	startOfTxId     = 42 + 1 + 9 + 1
	endOfTxId       = 42 + 1 + 9 + 1 + 5
)

func getAppearances(addrStr string, lines []string, lastVisited uint32, found int) *[]types.AppRecord {
	results := make([]types.AppRecord, 0, 1000)
	for idx := found; idx < len(lines); idx++ {
		if !strings.HasPrefix(lines[idx], addrStr) {
			break
		}
		strippedBn := strings.TrimLeft(lines[idx][startOfBlockNum:endOfBlockNum], "0")
		strippedTxId := strings.TrimLeft(lines[idx][startOfTxId:endOfTxId], "0")
		r := types.AppRecord{
			BlockNumber:      uint32(base.MustParseUint64(strippedBn)),
			TransactionIndex: uint32(base.MustParseUint64(strippedTxId)),
		}
		if r.BlockNumber > lastVisited {
			results = append(results, r)
		}
	}
	if len(results) == 0 {
		return nil
	}
	return &results
}

// moveAllToProduction completes the update by moving the monitor files from ./cache/<chain>/monitors/staging to
// ./cache/<chain>/monitors.
func (updater *MonitorUpdate) moveAllToProduction() error {
	for _, mon := range updater.MonitorMap {
		err := mon.MoveToProduction()
		if err != nil {
			return err
		}
	}
	return nil
}
