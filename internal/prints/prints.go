package prints

import (
	"os"
	"fmt"

	"github.com/fatih/color"
)

var IsQuiet = false
var warnBegin = color.YellowString("Warning: ")

func Warnln(items ...any) {
	fmt.Fprint(os.Stderr, warnBegin)
	fmt.Fprintln(os.Stderr, items...)
}

func Warnf(format string, parameters ...any) {
	fmt.Fprint(os.Stderr, warnBegin)
	fmt.Fprintf(os.Stderr, format, parameters...)
}

func Print(items ...any) {
	if (IsQuiet) {
		return
	}

	fmt.Print(items...)
}

func Println(items ...any) {
	if (IsQuiet) {
		return
	}

	fmt.Println(items...)
}

func Printf(format string, parameters ...any) {
	if (IsQuiet) {
		return
	}

	fmt.Printf(format, parameters...)
}
