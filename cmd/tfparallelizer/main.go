package main

import (
	"flag"
	"fmt"

	"github.com/HASHIMOTO-Takafumi/tfparallelizer/internal"
)

func main() {
	flag.Parse()

	var c = flag.Arg(0)
	var s = flag.Arg(1)
	var a = flag.Arg(2)
	var b = flag.Arg(3)

	if b == "" {
		fmt.Printf("4 arguments required")
		return
	}

	comparer := internal.New(c, s)

	comparer.Compare(a, b)
}
