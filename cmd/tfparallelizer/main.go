package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/HASHIMOTO-Takafumi/tfparallelizer/internal"
)

func main() {
	var c = flag.String("c", "", "YAML configuration file")

	flag.Usage = usage
	flag.Parse()

	var s = flag.Arg(0)
	var a = flag.Arg(1)
	var b = flag.Arg(2)

	comparer := internal.New(*c, s)

	comparer.Compare(a, b)
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s [flags] schema.json left_tfstate.json right_tfstate.json\n", os.Args[0])
	flag.PrintDefaults()
}
