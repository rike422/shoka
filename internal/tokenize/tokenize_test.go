package tokenize_test

import (
	"strings"
	"testing"

	"github.com/rike422/shoka/internal/tokenize"
)

func TestCamelCase(t *testing.T) {
	got := tokenize.Text("getUserProfile")
	tokens := strings.Fields(got)
	for _, want := range []string{"getuserprofile", "get", "user", "profile"} {
		if !containsToken(tokens, want) {
			t.Fatalf("Text(%q) = %q, missing token %q", "getUserProfile", got, want)
		}
	}
}

func TestCamelCaseAcronym(t *testing.T) {
	got := tokenize.Text("parseJSONValue")
	tokens := strings.Fields(got)
	for _, want := range []string{"parse", "json", "value", "parsejsonvalue"} {
		if !containsToken(tokens, want) {
			t.Fatalf("missing token %q in %q", want, got)
		}
	}
}

func TestSnakeCase(t *testing.T) {
	got := tokenize.Text("get_user_profile")
	tokens := strings.Fields(got)
	for _, want := range []string{"get", "user", "profile"} {
		if !containsToken(tokens, want) {
			t.Fatalf("missing token %q in %q", want, got)
		}
	}
}

func TestDigitsInIdent(t *testing.T) {
	got := tokenize.Text("utf8Reader")
	for _, want := range []string{"utf8", "reader", "utf8reader"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestJapaneseBigrams(t *testing.T) {
	got := tokenize.Text("有効期限")
	for _, want := range []string{"有効期限", "有効", "効期", "期限"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	q := tokenize.QueryText("有効期限")
	if strings.Contains(q, "有効期限") {
		t.Fatalf("query should not require full CJK run: %q", q)
	}
	for _, want := range []string{"有効", "効期", "期限"} {
		if !strings.Contains(q, want) {
			t.Fatalf("query missing %q in %q", want, q)
		}
	}
}

func TestMixedJapaneseEnglish(t *testing.T) {
	got := tokenize.Text("期限のuserProfile処理")
	for _, want := range []string{"期限", "user", "profile", "処理"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
}

func TestFTS5MatchSafe(t *testing.T) {
	m, ok := tokenize.FTS5Match(`foo OR bar`)
	if !ok {
		t.Fatal("expected match")
	}
	if !strings.Contains(m, `"foo"`) || !strings.Contains(m, `"or"`) || !strings.Contains(m, `"bar"`) {
		t.Fatalf("unexpected match expr: %s", m)
	}
	if !strings.Contains(m, " AND ") {
		t.Fatalf("expected AND join: %s", m)
	}
}

func TestFTS5MatchEscapesQuotes(t *testing.T) {
	// Quotes in the query are separators; tokens are still wrapped as FTS literals.
	m, ok := tokenize.FTS5Match(`say "hi"`)
	if !ok {
		t.Fatal("expected match")
	}
	if m != `"say" AND "hi"` {
		t.Fatalf("got %q", m)
	}
}

func TestFTS5MatchEmpty(t *testing.T) {
	if _, ok := tokenize.FTS5Match("   "); ok {
		t.Fatal("expected empty")
	}
	if _, ok := tokenize.FTS5Match("!!!"); ok {
		t.Fatal("expected empty for symbols-only")
	}
}

func TestPathTerms(t *testing.T) {
	got := tokenize.PathTerms("internal/auth/UserService.go")
	for _, want := range []string{"user", "service", "auth"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	base := tokenize.BasenameTerms("internal/auth/UserService.go")
	if !strings.Contains(base, "userservice") && !strings.Contains(base, "user") {
		t.Fatalf("basename weak: %q", base)
	}
	if strings.Contains(base, "internal") {
		t.Fatalf("basename should not include dirs: %q", base)
	}
}

func TestQualifiedNames(t *testing.T) {
	got := tokenize.Text("call pkg.Type and Foo::bar now")
	for _, want := range []string{"pkg", "type", "foo", "bar", "pkgtype", "foobar"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	// index-time keeps dotted/colon forms
	if !strings.Contains(got, "pkg.type") && !strings.Contains(got, "foo::bar") {
		t.Fatalf("missing qualified forms in %q", got)
	}
	q := tokenize.QueryText("pkg.Type")
	if strings.Contains(q, "pkg.type") || strings.Contains(q, "pkg::type") {
		t.Fatalf("query should not require punctuated form: %q", q)
	}
	if !strings.Contains(q, "pkgtype") || !strings.Contains(q, "pkg") || !strings.Contains(q, "type") {
		t.Fatalf("query missing pieces: %q", q)
	}
}

func TestDedupTokens(t *testing.T) {
	got := tokenize.Text("user user user")
	if strings.Count(got, "user") != 1 {
		t.Fatalf("expected dedup: %q", got)
	}
}

func containsToken(tokens []string, want string) bool {
	for _, t := range tokens {
		if t == want {
			return true
		}
	}
	return false
}
