// SPEC: _spec/internal/ptyproxy/pty-ownership.puml
package testx

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"
)

func RawTTY(label, ready string) error {
	fd := int(os.Stdin.Fd())
	old, err := term.MakeRaw(fd)
	if err != nil {
		return err
	}
	defer func() { _ = term.Restore(fd, old) }()
	if _, err := fmt.Fprint(os.Stdout, ready); err != nil {
		return err
	}
	var b [1]byte
	for {
		_, err := io.ReadFull(os.Stdin, b[:])
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(os.Stdout, "%s:%02x\n", label, b[0]); err != nil {
			return err
		}
	}
}
