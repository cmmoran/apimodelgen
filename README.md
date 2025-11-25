# apimodelgen

`apimodelgen` is a Go code generator that scans your project for structs and produces API-facing DTOs alongside optional patch counterparts. It relies on [`cobra`](https://github.com/spf13/cobra) for the CLI and [`viper`](https://github.com/spf13/viper) for configuration, so you can drive generation through flags, config files, or environment variables.

## Installation

```bash
go install github.com/cmmoran/apimodelgen@latest
```

Alternatively, clone the repository and run the binary locally from the repo root:

```bash
go run .
```

## Basic usage

The CLI exposes a single subcommand that performs parsing and code generation:

```bash
apimodelgen init [flags]
```

Example:

```bash
apimodelgen init \
  --input-directory ./internal/models \
  --output-directory ./api \
  --output-file api_gen.go \
  --suffix DTO \
  --patch-suffix Patch \
  --exclude-types DeprecatedModel,UnusedType \
  --exclude-tags "gorm:embedded" \
  --exclude-deprecated
```

This scans `./internal/models`, builds DTOs plus `Patch` versions for each DTO, and writes the generated code to `./api/api_gen.go`.

## Flags

Global flags (available on every command):

- `--level, -l` – Configure the log level (`trace`, `debug`, `info`, `warn`, `error`, etc.).
- `--config` – One or more config files. When multiple files are provided, later entries override earlier ones.

`init` flags:

- `--input-directory, -i` – Directory to scan for Go source (default: current working directory).
- `--output-directory, -o` – Directory where generated files are written (default: `api`).
- `--output-file, -f` – Filename for the generated DTOs (default: `api_gen.go`).
- `--suffix, -s` – Suffix appended to generated DTO type names.
- `--patch-suffix` – Suffix appended to generated patch types (default: `Patch`).
- `--keep-orm-tags, -k` – Preserve ORM-related struct tags on generated fields.
- `--flatten-embedded, -F` – Promote embedded/inline fields into the parent struct (enabled by default).
- `--include-embedded, -E` – Keep embedded structs as their own fields instead of flattening (mutually exclusive with `--flatten-embedded`).
- `--exclude-deprecated, -d` – Skip structs whose leading comments contain "deprecated".
- `--exclude-types, -t` – Comma-separated list of type names to skip (case-insensitive).
- `--exclude-tags, -T` – Comma-separated list of tag filters formatted as `key:value` (e.g., `gorm:embedded`) used to exclude fields or referenced types.

> **Note:** `--flatten-embedded` and `--include-embedded` cannot both be enabled; the last one set wins.

## Configuration files and environment variables

`viper` automatically reads environment variables matching flag names (e.g., `LEVEL`, `INPUT_DIRECTORY`) and merges configuration from files. By default, the CLI looks for a `config.yaml` in the current directory or `/etc`. You can specify one or more explicit files with `--config`; when multiple files are provided, they are merged in order, with later files overriding earlier ones.

You can also supply a version string via the `--version` build variable (e.g., `go build -ldflags "-X github.com/cmmoran/apimodelgen/cmd.version=1.2.3"`).

A minimal YAML config might look like:

```yaml
in_dir: ./internal/models
out_dir: ./api
out_file: api_gen.go
suffix: DTO
patch_suffix: Patch
keep_orm_tags: false
flatten_embedded: true
exclude_types:
  - DeprecatedModel
exclude_by_tags:
  - key: gorm
    value: embedded
```

## Output

Running `apimodelgen init` renders the generated code to the configured output path, creating the directory if necessary. DTO structs are derived from your input types, and patch structs are synthesized by pointerizing fields or wrapping slices so partial updates can be expressed.
