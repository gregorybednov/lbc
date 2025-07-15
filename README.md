# lbc

Experimental blockchain for social cooperation implementation built on Tendermint and BadgerDB.

## Building

This project uses Go modules. Ensure Go 1.24 or newer is installed and run:

```bash
go build ./...
```

## Usage

The binary exposes several flags. To generate configuration files and keys for a
new node run:

```bash
go run . init genesis
```

For joining an existing network use `init join` instead:
```bash
go run . init join <path to genesis.json>
```

Once configured you can start the node with:

```bash
go run .
```

To verify Yggdrasil connectivity without starting Tendermint you can run:

```bash
go run . testYggdrasil
```

See `--help` for a full list of available options.
