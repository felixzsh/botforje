package main

import (
	"fmt"
	"os"
)

func dispatch(node *commandHelp, path, args []string) int {
	if len(args) == 0 {
		printHelp(os.Stderr, path...)
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		printHelp(os.Stdout, path...)
		return 0
	case "--version":
		// The only global flag: everything else lives on its subcommand
		// (ADR 0025).
		fmt.Printf("botflota %s\n", version)
		return 0
	}
	child := childNamed(node, args[0])
	if child == nil {
		fmt.Fprintf(os.Stderr, "unknown %s %q\n\n", subject(path), args[0])
		printHelp(os.Stderr, path...)
		return 2
	}
	childPath := append(append([]string{}, path...), child.name)
	if len(child.sub) > 0 {
		return dispatch(child, childPath, args[1:])
	}
	return child.run(args[1:])
}

func subject(path []string) string {
	if len(path) == 0 {
		return "command"
	}
	return path[len(path)-1] + " subcommand"
}
