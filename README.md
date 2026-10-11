apiexport-pie
===

Visualize [kcp](https://www.kcp.io/) APIExports and relations, without torching the earth for LLM tokens.

## Purpose

The outputs show which kinds ship together in an APIExport, which workspaces get them, and what
each export's provider may do there through its claims.

`--diff` compares two versions of a tree: a PR branch against main, or one release tag against the
next.

## Install

```sh
go build .
```

### Dependencies

Drawing images requires [d2](https://d2lang.com) on PATH. A git ref as a tree needs git.

### Tests

```sh
go test ./...
go test -tags integration ./...
```

The integration tests draw with d2, and verify `root` and `root:universal` assumptions baked into the tool against kcp.

## Usage

```
apiexport-pie [-v] [--style classic|overview] [-o FILE [--watch]] TREE
apiexport-pie [-v] --surface [-o FILE [--watch]] TREE
apiexport-pie [-v] --diff [--surface|--style overview [-o FILE]] A B
```

TREE, A and B are each a directory of manifests or `[REMOTE#]REF:PATH`: PATH at commit REF of this
repository, or of REMOTE (a remote name, URL or path). `--watch` needs a directory.

## Flags

| Flag | Effect |
|---|---|
| `--style STYLE` | Diagram style: `classic` (default) or `overview`. Not with `--surface`; with `--diff`, `overview` only. |
| `--surface` | The surface table instead of the diagram. |
| `--diff` | What changes from tree A to tree B. With `--surface`, both tables as one; with `--style overview`, both trees as one overview. |
| `-o FILE` | Write to FILE instead of stdout; the extension picks the format (below). With `--diff`, only with `--surface` or `--style overview`. |
| `--watch` | With `-o FILE.d2`: rewrite FILE when a manifest changes and show it with `d2 --watch`. Not with `--diff`. |
| `-v` | Also list the documents that are not drawn. |
| `-h` | Usage and flags. |

| `-o` FILE ends in | Output |
|---|---|
| `.svg`, `.png`, `.pdf` | drawn by d2 (PNG and PDF force a browser download on first use) |
| `.d2` | d2 source |
| other file extension | d2 source; markdown with `--surface` |

## Outputs

- **classic** (default): a table per consumer (workspaces or a
  directory of APIBinding files) and per APIExport, with arrows from bindings to exports and from claims to the claimed kinds.
- **overview**: three columns: CONSUMERS -> APIEXPORTS -> RESOURCES.
- **surface**: a row per kind and per set of claims, columns generated per consumer.
- **diff**: `added:`, `removed:`, `moved:`, `changed:` lines for exports, kinds, claims, consumers and APIBindings.

Warnings and notices are printed to stdout. Any kind in more than one APIExport is flagged with orange.
