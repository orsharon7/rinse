package ignore

import (
	"testing"
)

func TestMatchesBasename(t *testing.T) {
	m := ParsePatterns([]string{"*.pb.go", "*.gen.go"})

	cases := []struct {
		path string
		want bool
	}{
		{"foo.pb.go", true},
		{"internal/api/foo.pb.go", true},
		{"internal/api/foo.gen.go", true},
		{"internal/api/foo.go", false},
		{"vendor/foo.go", false}, // vendor/ not in patterns above
	}
	for _, c := range cases {
		got := m.Matches(c.path)
		if got != c.want {
			t.Errorf("Matches(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}

func TestMatchesDirectoryPrefix(t *testing.T) {
	m := ParsePatterns([]string{"vendor/", "internal/mocks/"})

	cases := []struct {
		path string
		want bool
	}{
		{"vendor/github.com/foo/bar.go", true},
		{"vendor/foo.go", true},
		{"internal/mocks/service_mock.go", true},
		{"internal/mocks/", true},
		{"internal/service.go", false},
		{"src/vendor_util.go", false}, // "vendor/" should not match this
	}
	for _, c := range cases {
		got := m.Matches(c.path)
		if got != c.want {
			t.Errorf("Matches(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}

func TestMatchesFullPath(t *testing.T) {
	m := ParsePatterns([]string{"internal/db/migrations/*.sql"})

	cases := []struct {
		path string
		want bool
	}{
		{"internal/db/migrations/001_init.sql", true},
		{"internal/db/migrations/002_users.sql", true},
		{"internal/db/schema.sql", false},
	}
	for _, c := range cases {
		got := m.Matches(c.path)
		if got != c.want {
			t.Errorf("Matches(%q) = %v; want %v", c.path, got, c.want)
		}
	}
}

func TestMatchesComments(t *testing.T) {
	m := ParsePatterns([]string{
		"# Generated files",
		"",
		"*.pb.go",
		"  ", // whitespace-only treated as blank
	})
	if !m.Matches("foo.pb.go") {
		t.Error("expected foo.pb.go to be matched")
	}
	if m.Matches("foo.go") {
		t.Error("expected foo.go not to be matched")
	}
}

func TestEmptyMatcher(t *testing.T) {
	var m Matcher
	if m.Matches("anything.go") {
		t.Error("empty Matcher should match nothing")
	}
}

func TestMatchesLeadingDotSlash(t *testing.T) {
	m := ParsePatterns([]string{"vendor/"})
	if !m.Matches("./vendor/foo.go") {
		t.Error("leading ./ should be stripped before matching")
	}
}
