package main

import (
	"fmt"
	"io"
)

// errWriter wraps an io.Writer so a long sequence of writes can be
// followed by a single error check. The first failure latches and
// short-circuits subsequent calls. Used by flag.Usage handlers and
// other CLI write blocks where peppering each Fprintln with an
// inline err check would obscure the layout.
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
