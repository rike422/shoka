package limits

const (
	MaxFileBytes     = 1 << 20 // 1 MiB
	MaxLineBytes     = 16 << 10
	ChunkLines       = 80
	ChunkOverlap     = 10
	MaxChunkBytes    = 64 << 10
	DefaultTopK      = 10
	MaxTopK          = 50
	MaxSnippetRunes  = 400
	MaxChunksPerFile = 2
	SchemaVersion    = "3"
	TokenizerVersion = "3"
)
