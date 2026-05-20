package render

import (
	"fmt"
	"io"
)

// errWriter wraps an io.Writer so a long sequence of writes can be
// followed by a single error check at the end. The first failure
// latches and short-circuits subsequent calls. Pattern from Rob Pike,
// "Errors are values" — keeps render code linear instead of pebbled
// with err returns for every fmt.Fprintf.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Print(a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprint(e.w, a...)
}

func (e *errWriter) Println(a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintln(e.w, a...)
}

func (e *errWriter) Printf(format string, a ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, a...)
}

func (e *errWriter) WriteString(s string) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, s)
}
