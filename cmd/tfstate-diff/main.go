package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/HASHIMOTO-Takafumi/tfstate-diff/internal"
)

func main() {
	var verbose = flag.Bool("v", false, "be verbose")
	var c = flag.String("c", "", "YAML configuration file")

	flag.Usage = usage
	flag.Parse()

	if flag.NArg() < 3 {
		usage()
		return
	}

	var s = flag.Arg(0)
	var l = flag.Arg(1)
	var r = flag.Arg(2)

	comparer, err := internal.New(*c, s)
	if err != nil {
		fmt.Println(err)
		return
	}

	if *verbose {
		comparer.SetDetailWriter(os.Stdout)
	}

	if err = comparer.Compare(l, r); err != nil {
		fmt.Println(err)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "usage: %s [flags] schema.json left_tfstate.json right_tfstate.json\n", os.Args[0])
	flag.PrintDefaults()
}
