## Reth-compatible v6 fork

This branch starts at upstream v6.7.0. It preserves the Reth archive/state and
RPC batch fixes from the v5 deployment, accepts nullable reward-trace hashes,
and prevents partial chunk downloads or failed index reads from silently
advancing address monitors. The latter two changes derive from
[banteg's trace fix](https://github.com/TrueBlocks/trueblocks-chifra/pull/2) and
[chunk-integrity fix](https://github.com/TrueBlocks/trueblocks-chifra/pull/1).

Counts and bounds use independent pagination for each address. Unrestricted
counts read the monitor count directly. Bounds scan the records to remain correct
for unsorted legacy monitors. The API's default 250-record page no longer truncates
these summaries. Explicit bounds limits still select
a subset. Missing or corrupt index data surfaces as an error rather than proof
of no activity. These fixes do not repair gaps already stored in old monitors;
compare any suspect monitor with an independent replay before replacing it.

Build with Go 1.25.1 or newer:

```sh
GOWORK=off CGO_ENABLED=0 go build -trimpath -o chifra .
```

Before a v5 cutover, preserve the previous executable, configuration, service
settings, and address monitors. Validate this build against the configured RPC
and a copy of an existing index chunk, including counts over 250, both batch
orders, full first/latest bounds, archive proof, and uncached tracing. Use an
isolated cache for replay checks. Keep the index/cache paths and private RPC
configuration outside the repository. Switch the API and scraper to the same
immutable executable, verify scrape progress, and retain the prior release for
rollback. Do not initialize or reseed an existing index as part of an upgrade.

## chifra

`chifra` is an command line tool for accessing the entire collection of TrueBlocks tools. Enter `chifra <tool> --help` for more information.

```[shell]
Purpose:
  Access to all TrueBlocks tools (chifra <cmd> --help for more).

  Accounts:
    list          list every appearance of an address anywhere on the chain
    export        export full details of transactions for one or more addresses
    monitors      add, remove, clean, and list address monitors
    names         query addresses or names of well-known accounts
    abis          fetches the ABI for a smart contract
  Chain Data:
    blocks        retrieve one or more blocks from the chain or local cache
    transactions  retrieve one or more transactions from the chain or local cache
    receipts      retrieve receipts for the given transaction(s)
    logs          retrieve logs for the given transaction(s)
    traces        retrieve traces for the given transaction(s)
    when          find block(s) based on date, blockNum, timestamp, or 'special'
  Chain State:
    state         retrieve account balance(s) for one or more addresses at given block(s)
    tokens        retrieve token balance(s) for one or more addresses at given block(s)
  Admin:
    config        report on the status of the TrueBlocks system
    daemon        initialize and control long-running processes such as the API and the scrapers
    scrape        scan the chain and update the TrueBlocks index of appearances
    chunks        manage and investigate chunks and bloom filters
    init          initialize the TrueBlocks system by downloading from IPFS
  Other:
    explore       open an explorer for one or more addresses, blocks, or transactions
    slurp         fetch data from Etherscan for any address
  Flags:
    -h, --help    display this help screen

  Use "chifra [command] --help" for more information about a command.
```

*Copyright (c) 2024, TrueBlocks, LLC. All rights reserved. Generated with goMaker.*
