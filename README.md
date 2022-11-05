# Temporal CLI

[![Go Reference](https://pkg.go.dev/badge/github.com/temporalio/cli.svg)](https://pkg.go.dev/github.com/temporalio/cli)
[![ci](https://github.com/temporalio/cli/actions/workflows/ci.yml/badge.svg)](https://github.com/temporalio/cli/actions/workflows/ci.yml)

> ⚠️ This project is experimental and not suitable for production use. ⚠️

Temporal CLI is a distribution of [Temporal](https://github.com/temporalio/temporal) that runs as a single process with zero runtime dependencies.

## Getting Started

### Download and Start Temporal Server Locally

Download and extract the [latest release](https://github.com/temporalio/cli/releases/latest) from [GitHub releases](https://github.com/temporalio/cli/releases).

Start Temporal server:

```bash
temporal server start-dev --namespace default
```

At this point you should have a server running on `localhost:7233` and a web interface at <http://localhost:8233>.

Run individual commands to interact with the local Temporal server.

```bash
temporal namespace list
temporal workflow list
```

## Configuration

Use the help flag to see all available options:

```bash
temporal server start-dev -h
```

### Namespace Registration

Namespaces can be pre-registered at startup so they're available to use right away:

```bash
temporal server start-dev --namespace foo --namespace bar
```

Registering namespaces the old-fashioned way via `temporal namespace register foo` works too!

### Persistence Modes

### In-memory

By default `temporal server start-dev` run in an in-memory mode.

#### File on Disk

To persist the state to a file use `--db-filename`:

```bash
temporal server start-dev --db-filename my_test.db
```

### Temporal UI

By default the Temporal UI is started with Temporal CLI. The UI can be disabled via a runtime flag:

```bash
temporal server start-dev --headless
```

To build without static UI assets, use the `headless` build tag when running `go build`.

### Dynamic Config

Some advanced uses require Temporal dynamic configuration values which are usually set via a dynamic configuration file inside the Temporal configuration file. Alternatively, dynamic configuration values can be set via `--dynamic-config-value KEY=JSON_VALUE`.

For example, to disable search attribute cache to make created search attributes available for use right away:

```bash
temporal server start-dev --dynamic-config-value system.forceSearchAttributesCacheRefreshOnRead=true
```

## Development

To compile the source run:

```bash
go build -o dist/temporal ./cmd/temporal
```

To run all tests:

```bash
go test ./...
```

## Known Issues

-   When consuming Temporal as a library in go mod, you may want to replace grpc-gateway with a fork to address URL escaping issue in UI. See <https://github.com/temporalio/temporalite/pull/118>
