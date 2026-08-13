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

## tree-sitter-ruby
- Package: github.com/tree-sitter/tree-sitter-ruby@v0.23.1
- License: MIT

## tree-sitter-javascript
- Package: github.com/tree-sitter/tree-sitter-javascript@v0.25.0
- License: MIT

## tree-sitter-typescript
- Package: github.com/tree-sitter/tree-sitter-typescript@v0.23.2
- License: MIT
- Note: provides both TypeScript and TSX grammars (`LanguageTypescript` / `LanguageTSX`).

## tags.scm
- Go / Python / Ruby / JavaScript / TypeScript / TSX: simplified definition-only queries derived from upstream `queries/tags.scm` (no `@reference`, no predicates).
- GDScript: authored for shoka under `internal/treesitter/queries/gdscript/tags.scm`.
