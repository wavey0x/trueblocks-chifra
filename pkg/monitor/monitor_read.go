package monitor

// Copyright 2021 The TrueBlocks Authors. All rights reserved.
// Use of this source code is governed by a license that can
// be found in the LICENSE file.

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"

	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/file"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/index"
	"github.com/TrueBlocks/trueblocks-chifra/v6/pkg/types"
)

// ReadMonitorHeader reads the monitor's header and returns without closing the file
func (mon *Monitor) ReadMonitorHeader() (err error) {
	if mon.ReadFp == nil {
		mon.ReadFp, err = os.OpenFile(mon.Path(), os.O_RDONLY, 0644)
		if err != nil {
			return
		}
	}

	if file.FileSize(mon.Path()) > 0 {
		return binary.Read(mon.ReadFp, binary.LittleEndian, &mon.Header)
	}

	return
}

// ReadAppearanceAt returns the appearance at the one-based index. The file remains open.
func (mon *Monitor) ReadAppearanceAt(idx int64, app *types.AppRecord) (err error) {
	if idx == 0 || idx > mon.Count() {
		// the file contains a header one record wide, so a one-based index eases caller code
		err = fmt.Errorf("index out of range in ReadAppearanceAt[%d]", idx)
		return
	}

	if mon.ReadFp == nil {
		path := mon.Path()
		mon.ReadFp, err = os.OpenFile(path, os.O_RDONLY, 0644)
		if err != nil {
			return
		}
	}

	// This index is one based because we have to skip over the header
	byteIndex := int64(idx) * index.AppRecordWidth
	_, err = mon.ReadFp.Seek(byteIndex, io.SeekStart)
	if err != nil {
		return
	}

	err = binary.Read(mon.ReadFp, binary.LittleEndian, &app.BlockNumber)
	if err != nil {
		return
	}
	err = binary.Read(mon.ReadFp, binary.LittleEndian, &app.TransactionIndex)
	return
}

// ReadEndpoints returns the earliest and latest appearances in the monitor and the number of
// records. Monitors written by older versions of freshen are not always in block order, so
// this scans every record rather than reading the first and last positions.
func (mon *Monitor) ReadEndpoints() (first, last types.AppRecord, count int64, err error) {
	count = mon.Count()
	if count == 0 {
		return
	}

	f, err := os.Open(mon.Path())
	if err != nil {
		return
	}
	defer f.Close()
	if _, err = f.Seek(index.AppRecordWidth, io.SeekStart); err != nil {
		return
	}

	reader := bufio.NewReaderSize(f, 1<<16)
	var buf [index.AppRecordWidth]byte
	var minKey, maxKey uint64
	for i := int64(0); i < count; i++ {
		if _, err = io.ReadFull(reader, buf[:]); err != nil {
			return types.AppRecord{}, types.AppRecord{}, 0, err
		}
		app := types.AppRecord{
			BlockNumber:      binary.LittleEndian.Uint32(buf[0:4]),
			TransactionIndex: binary.LittleEndian.Uint32(buf[4:8]),
		}
		key := uint64(app.BlockNumber)<<32 | uint64(app.TransactionIndex)
		if i == 0 || key < minKey {
			first, minKey = app, key
		}
		if i == 0 || key > maxKey {
			last, maxKey = app, key
		}
	}
	return
}
