package treesitter

import (
	"strings"
	"unicode/utf8"

	sitter "github.com/tree-sitter/go-tree-sitter"
)

const maxSignatureLines = 3
const maxSignatureRunes = 400

// Extract returns definition symbols for src. On per-file failure it returns nil, err;
// callers may treat err as soft (keep chunk indexing).
func (e *Extractor) Extract(relPath string, src []byte) ([]Symbol, error) {
	if e == nil || len(e.byLang) == 0 {
		return nil, nil
	}
	lang, ok := e.LanguageForPath(relPath)
	if !ok {
		return nil, nil
	}
	eng := e.byLang[lang]
	parser := sitter.NewParser()
	defer parser.Close()
	if err := parser.SetLanguage(eng.language); err != nil {
		return nil, nil
	}
	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, nil
	}
	defer tree.Close()

	cursor := sitter.NewQueryCursor()
	defer cursor.Close()
	matches := cursor.Matches(eng.query, tree.RootNode(), src)
	capNames := eng.query.CaptureNames()

	var out []Symbol
	for m := matches.Next(); m != nil; m = matches.Next() {
		var defName, kind string
		var defNode, nameNode *sitter.Node
		for _, cap := range m.Captures {
			cname := capNames[cap.Index]
			if cname == "name" {
				n := cap.Node
				nameNode = &n
				continue
			}
			if k, ok := kindFromCapture(cname); ok {
				kind = k
				n := cap.Node
				defNode = &n
			}
		}
		if defNode == nil || nameNode == nil || kind == "" {
			continue
		}
		defName = strings.TrimSpace(nameNode.Utf8Text(src))
		if defName == "" {
			continue
		}
		start := int(defNode.StartPosition().Row) + 1
		end := int(defNode.EndPosition().Row) + 1
		if end < start {
			end = start
		}
		out = append(out, Symbol{
			Name:      defName,
			Kind:      kind,
			StartLine: start,
			EndLine:   end,
			Signature: rawSignature(src, start),
		})
	}
	return out, nil
}

func rawSignature(src []byte, startLine1 int) string {
	if startLine1 < 1 {
		startLine1 = 1
	}
	text := string(src)
	lines := strings.Split(text, "\n")
	idx := startLine1 - 1
	if idx >= len(lines) {
		return ""
	}
	end := idx + maxSignatureLines
	if end > len(lines) {
		end = len(lines)
	}
	sig := strings.Join(lines[idx:end], "\n")
	sig = strings.TrimRight(sig, "\r")
	if utf8.RuneCountInString(sig) <= maxSignatureRunes {
		return sig
	}
	runes := []rune(sig)
	return string(runes[:maxSignatureRunes])
}
