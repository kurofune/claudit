# Go Code Intelligence — Use the LSP Tool

The `LSP` tool is backed by `gopls` (plugin `gopls-lsp@claude-plugins-official`, enabled in `.claude/settings.json`). It is a first-class tool for Go symbol queries — reach for it instead of grep when the question is about Go semantics.

## Loading

`LSP` is a **deferred tool**: its schema is not present at session start. Before first use each session, load it:

```
ToolSearch select:LSP
```

After that, call it like any other tool.

## Decision rule — LSP vs. grep

| Query shape | Tool |
|---|---|
| Where is `<GoSymbol>` defined? | **LSP** `goToDefinition` (or `workspaceSymbol` to locate by name first) |
| Who calls `<GoFunction>`? | **LSP** `findReferences` / `incomingCalls` |
| What does `<GoFunction>` call? | **LSP** `outgoingCalls` |
| What's the type / docstring of this symbol? | **LSP** `hover` |
| What implements `<GoInterface>`? | **LSP** `goToImplementation` |
| What symbols does this file expose? | **LSP** `documentSymbol` |
| Find a string / regex across mixed file types (TOML, MD, comments, string literals) | **grep** |

Rule of thumb: if the question is "where is this Go symbol defined / who calls it / what's its type," use LSP. If the question is "find this string / find this pattern," use grep. If both fit, prefer LSP for `.go` files and grep for everything else.

## Position-based vs. name-based

Most LSP ops require `filePath` + `line` + `character` (1-based, as shown in editors) — they operate on a position, not a name. The natural flow when starting from a name:

1. `LSP workspaceSymbol` with the name → get candidate `file:line` results, OR
2. `grep -rn 'SymbolName' --include='*.go'` → pick one occurrence, then
3. `LSP goToDefinition` / `findReferences` / `hover` from that position.

`workspaceSymbol` is the only name-based entrypoint. Everything else expects coordinates.

## When LSP is unavailable

If `ToolSearch select:LSP` returns no matches, or an LSP call returns an error about no server being available for the file type, fall back to grep and note the limitation in your reply. Don't silently downgrade.
