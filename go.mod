module github.com/rike422/shoka

go 1.25.5

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/mark3labs/mcp-go v0.58.0
	github.com/mattn/go-sqlite3 v1.14.32
	github.com/rike422/shoka/third_party/tree_sitter/tree-sitter-gdscript v0.0.0
	github.com/tree-sitter/go-tree-sitter v0.25.0
	github.com/tree-sitter/tree-sitter-go v0.25.0
	github.com/tree-sitter/tree-sitter-javascript v0.25.0
	github.com/tree-sitter/tree-sitter-python v0.25.0
	github.com/tree-sitter/tree-sitter-ruby v0.23.1
	github.com/tree-sitter/tree-sitter-typescript v0.23.2
)

require (
	github.com/google/jsonschema-go v0.4.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-pointer v0.0.1 // indirect
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.2 // indirect
	github.com/spf13/cast v1.7.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/text v0.14.0 // indirect
)

replace github.com/rike422/shoka/third_party/tree_sitter/tree-sitter-gdscript => ./third_party/tree_sitter/tree-sitter-gdscript
