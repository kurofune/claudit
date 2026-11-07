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
		{"go build ./x && tmux new-session -d -s a", ""},
		{"go vet ./... && echo ok", "go vet"},
		{"go test ./... | tail -5", ""},
		{"cat > f <<'EOF'\ngo test\nEOF\ngo test ./...", "go test"},
		{"go test ./... ; echo done", ""},
		{"go test ./... || true", ""},
		{"go test ./... && printf 'ok\\n' && true && :", "go test"},
		{"go test ./... |& tee log", ""},
		{"go test ./... 2>&1", "go test"},
		{"go test ./...\n# done\n", "go test"},
		{"go test ./... # all of it", "go test"},
		{"time go test ./... | tail", ""},
		{"go test ./... && echo ok | false", ""},
		{"go test ./... && printf x | tee log", ""},

		{"echo '<<EOF'\ngo test ./...", "go test"},
		{"# example <<EOF\ngo test ./...", "go test"},
		{"echo \"<<EOF\"\ngo test ./...", "go test"},
		{"echo \\<<EOF\ngo test ./...", "go test"},
		{"cat <<EOF\ngo test ./...\nEOF", ""},
		{"cat <<EOF | sh\ngo test ./...\nEOF\nls", ""},
		{"cat <<A <<B\ngo test\nA\ngo vet\nB\nmake", "make"},
		{"cat <<EOF\ngo test ./...", ""},

		{"go build -o /tmp/rysh1-djinn ./cmd/djinn && for w in 160 46; do tmux kill-session -t rysh1-$w 2>/dev/null; done", ""},
		{"go test -short ./internal/djinn/ops/room/ 2>&1 | grep -E \"^(---|===)\" | head; git stash list | head -0", ""},
		{"go build -o /tmp/claudit-9bw ./cmd/claudit && (/tmp/claudit-9bw serve --open=false --port 18797 >/tmp/claudit-9bw.log 2>&1 &) ; for i in $(seq 1 30); do curl -s -o /dev/null http://127.0.0.1:18797/ && break; done", ""},
		{"npm test --silent 2>&1 | grep -E \"^ℹ (pass|fail)\"; go test -short ./... 2>&1 | grep -v \"^ok\" | head; git status --short", ""},
		{"GOOS=windows go vet ./internal/djinn/realm/ ./cmd/djinn/ && echo win-ok", "go vet"},
		{"python3 - <<'EOF'\nprint(1)\nEOF\ngo vet ./internal/registry/", "go vet"},
		{"cat >> internal/corpus/corpus_test.go <<'EOF'\nfunc TestX(t *testing.T) {}\nEOF\ngo test ./internal/corpus/", "go test"},

		{"go test ./... && echo ok >&-", ""},
		{"echo x >&- && go test ./...", ""},
		{"go test ./... & false", ""},
		{"go test ./... &", ""},
		{"go test ./... > out.log", ""},
		{"go test ./... >&2", "go test"},
		{"go test ./... 1>&2", "go test"},
		{"go test ./... 2>&1 >&2", "go test"},
		{"go test ./... 2>&1 | tee out.log", ""},
		{"go test ./... >> out.log", ""},
		{"go test ./... 2>/dev/null", ""},
		{"go test ./... &> out.log", ""},
		{"go test ./... < in.txt", ""},
		{"go test ./... >&out.log", ""},
		{"go test ./... 2>&-", ""},
		{"go test ./... && echo ok > f", ""},
		{"go test ./... && echo ok &", ""},
		{"ls && go test ./... &", ""},
		{"sleep 1 & go test ./...", "go test"},
		{"sleep 1 &\ngo test ./...", "go test"},
		{"go test ./... 2>&1&", ""},
		{"echo 'a > b' && go test ./...", "go test"},
		{"echo \"a & b\" && go test ./...", "go test"},
		{"echo a \\> b && go test ./...", "go test"},
		{"go test ./... # > out.log &", "go test"},
		{"cat <<EOF > f\nx > y &\nEOF\ngo test ./...", "go test"},
		{"ls > f; go test ./...", "go test"},
		{"ls > f\ngo test ./...", "go test"},
		{"diff <(ls) <(ls) ; go vet ./...", "go vet"},

		{"", ""},
	}
	for _, tc := range cases {
		if got := checkKind(tc.cmd).kind; got != tc.want {
			t.Errorf("checkKind(%q) = %q, want %q", tc.cmd, got, tc.want)
		}
	}
}

// TestCheckKindGate: an earlier command joined by && may fail and stop the
// chain before the check runs; one joined by || may succeed and skip it.
func TestCheckKindGate(t *testing.T) {
	cases := []struct {
		cmd               string
		afterAnd, afterOr bool
	}{
		{"go test ./...", false, false},
		{"ls; go test ./...", false, false},
		{"ls\ngo test ./...", false, false},
		{"echo x && go test ./...", false, false},
		{"echo x && true && go test ./... && echo ok", false, false},
		{"go build ./cmd/djinn && go vet ./cmd/djinn ./internal/djinn/ops/room", true, false},
		{"cd web && npm test", true, false},
		{"ls; echo a && go test ./...", false, false},
		{"echo a | grep a && go test ./...", true, false},
		{"go build ./... &&\ngo vet ./...", true, false},
		{"go build ./... && # then vet\n  go vet ./...", true, false},
		{"go build ./... &&\n\ngo vet ./...", true, false},
		{"ls || cargo check", false, true},
		{"go build ./... && ls || make", true, true},
		{"go build ./... 2>&1 && go vet ./... 2>&1", true, false},
		{"sleep 1 & echo x && go test ./...", false, false},
	}
	for _, tc := range cases {
		got := checkKind(tc.cmd)
		if got.kind == "" || got.afterAnd != tc.afterAnd || got.afterOr != tc.afterOr {
			t.Errorf("checkKind(%q) = %+v, want a check with afterAnd=%v afterOr=%v", tc.cmd, got, tc.afterAnd, tc.afterOr)
		}
	}
}
