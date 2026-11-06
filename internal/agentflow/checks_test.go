package agentflow

import "testing"

func TestCheckKind(t *testing.T) {
	cases := []struct{ cmd, want string }{
		{"go test ./...", "go test"},
		{"go build ./...", "go build"},
		{"go vet ./...", "go vet"},
		{"npm test", "npm test"},
		{"npm run test", "npm run test"},
		{"npm run build", "npm run build"},
		{"npm run lint", "npm run lint"},
		{"npm run check", "npm run check"},
		{"make", "make"},
		{"make test", "make"},
		{"pytest -q", "pytest"},
		{"cargo test", "cargo test"},
		{"cargo build --release", "cargo build"},
		{"cargo check", "cargo check"},
		{"node --test jstest/", "node --test"},
		{"tsc --noEmit", "tsc"},
		{"git commit -m 'x'", "git commit"},
		{"git push origin main", "git push"},

		{"cd web && npm test", "npm test"},
		{"(cd x; go build ./...)", "go build"},
		{"CGO_ENABLED=0 go test ./...", "go test"},
		{"timeout 60 go vet ./...", "go vet"},
		{"time go test ./...", "go test"},
		{"A=1 B=2 timeout 30s make", "make"},
		{"git status\ngit commit -m x", "git commit"},
		{"ls || cargo check", "cargo check"},
		{"(make)", "make"},

		{"ps -A -o command | rg 'go test'", ""},
		{"echo go test", ""},
		{"grep -rn 'make' .", ""},
		{"go run ./cmd/x", ""},
		{"npm install", ""},
		{"npm run dev", ""},
		{"git status", ""},
		{"cd web", ""},
		{"maker", ""},
		{"gotest ./...", ""},
		{"python3 - <<'EOF'\nmake = 1\ngo test\nEOF", ""},
		{"cat > f.sh <<EOF\ngo build ./...\nEOF\ngo vet ./...", "go vet"},
		{"cat <<-\"END\"\n\tgit push\n\tEND\nls", ""},
		{"rg x <<< \"y\"\ngo test ./...", "go test"},
		{"rg 'prefix; go test ./...' .", ""},
		{"rg \"a && go build\" .", ""},
		{"echo 'x\ngo test'", ""},
		{"echo \"it's || make\"", ""},
		{"echo a\\; go test", ""},
		{"echo \"a\\\" ; go vet\"", ""},
		{"rg 'a;b' . ; go test ./...", "go test"},
		{"echo \"x\" && make", "make"},
		{"echo 'it'\"s\" || cargo check", "cargo check"},
		{"# don't skip checks\ngo test ./...", "go test"},
		{"# say \"hi\ngo build ./...", "go build"},
		{"ls # it's; fine\nmake", "make"},
		{"echo hi # a 'quote\ncargo check", "cargo check"},
		{"# go test ./...", ""},
		{"ls # ; go test", ""},
		{"echo a#b; go vet ./...", "go vet"},
		{"echo 'a # b; go test'", ""},
		{"echo \"x\" # 'y\nmake", "make"},
		{"(# c'\ngo test ./...)", "go test"},
		{"", ""},
	}
	for _, tc := range cases {
		if got := checkKind(tc.cmd); got != tc.want {
			t.Errorf("checkKind(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}
