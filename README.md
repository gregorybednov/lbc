# lbc
<<<<<<< HEAD
<<<<<<< HEAD
=======
>>>>>>> d8e9608 (Fix key parsing and update docs)

Experimental blockchain implementation built on Tendermint and BadgerDB.

## Building

This project uses Go modules. Ensure Go 1.24 or newer is installed and run:

```bash
go build ./...
```

## Usage

The binary exposes several flags. To generate configuration files and keys for a
new node run:

```bash
go run . --init --mode genesis
```

For joining an existing network use `--mode join` instead. Once configured you
can start the node with:

```bash
go run .
```

See `--help` for a full list of available options.
<<<<<<< HEAD
=======
blockchain for social cooperation
>>>>>>> 1e4be97 (Initial commit)
=======
>>>>>>> d8e9608 (Fix key parsing and update docs)
