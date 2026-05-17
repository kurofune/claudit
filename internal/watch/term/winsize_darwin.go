package term

// macOS / BSD encoding of the TIOCGWINSZ ioctl. Different from Linux —
// keep the per-platform constant in its own file rather than #ifdef'ing.
const tiocgwinsz = 0x40087468
