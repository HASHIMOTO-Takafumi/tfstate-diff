package main

import (
	"flag"
	"fmt"

	"github.com/HASHIMOTO-Takafumi/tfparallelizer/internal"
)

func main() {
	flag.Parse()

	var s = flag.Arg(0)
	var a = flag.Arg(1)
	var b = flag.Arg(2)

	if b == "" {
		fmt.Printf("3 arguments required")
		return
	}

	comparer := internal.New(s)

	comparer.Compare(a, b)
}
