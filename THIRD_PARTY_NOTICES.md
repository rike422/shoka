# Third-party notices

## tree-sitter runtime
- Package: github.com/tree-sitter/go-tree-sitter@v0.25.0
- License: MIT (see module)

## tree-sitter-go
- Package: github.com/tree-sitter/tree-sitter-go@v0.25.0
- License: MIT

## tree-sitter-python
- Package: github.com/tree-sitter/tree-sitter-python@v0.25.0
- License: MIT

## tree-sitter-gdscript
- Source: https://github.com/PrestonKnopp/tree-sitter-gdscript
- Vendored at: `third_party/tree_sitter/tree-sitter-gdscript/`
- Revision: see `third_party/tree_sitter/tree-sitter-gdscript/REVISION`
- License: MIT (copied as `LICENSE` in that directory)
- Note: vendored because the published Go module zip collides on case-insensitive filesystems (Swift binding paths).

## tags.scm
- Go / Python: simplified definition-only queries derived from upstream `queries/tags.scm` (no `@reference`).
- GDScript: authored for shoka under `internal/treesitter/queries/gdscript/tags.scm`.
