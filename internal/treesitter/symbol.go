package treesitter

// Symbol is one definition extracted at index time.
type Symbol struct {
	Name      string
	Kind      string // class, function, method, type, signal, ...
	StartLine int    // 1-based
	EndLine   int    // 1-based inclusive
	Signature string
}

const (
	KindClass    = "class"
	KindFunction = "function"
	KindMethod   = "method"
	KindType     = "type"
	KindSignal   = "signal"
)

func kindFromCapture(name string) (string, bool) {
	switch name {
	case "definition.class":
		return KindClass, true
	case "definition.function":
		return KindFunction, true
	case "definition.method":
		return KindMethod, true
	case "definition.type":
		return KindType, true
	case "definition.signal":
		return KindSignal, true
	case "definition.interface":
		return "interface", true
	case "definition.constant":
		return "constant", true
	case "definition.variable":
		return "variable", true
	case "definition.module":
		return "module", true
	default:
		return "", false
	}
}
