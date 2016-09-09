package main

import (
	"flag"
	"fmt"
	"os"
	"polymer"

	"os/user"
)

const VERSION = "0.10"

func main() {
	address := flag.String("address", "127.0.0.1", "listen address")
	port := flag.String("port", "5565", "listen port")
	prefix := flag.String("prefix", "/tmp", "output directory")
	version := flag.Bool("version", false, "output version and exit")
	flag.Parse()

	if *version {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	if owner, err := user.Current(); err == nil && owner.Uid == "0" {
		fmt.Println("This program can't be run as root!")
		os.Exit(1)
	}

	ch := make(chan string, 1024)

	tcpAddr := *address + ":" + *port
	input, err := polymer.NewTcpInput(tcpAddr, true, ch)

	if err != nil {
		fmt.Println(err.Error())
		return
	}

	output, err := polymer.NewFileOutput(*prefix, ch)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	go output.Run()
	go input.Run()

	fmt.Printf("Start working on %s, output file to %s.\n", tcpAddr, *prefix)

	select {}
}
