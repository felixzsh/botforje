package main

import (
	"errors"
	"flag"
	"os"
	"strings"
)

// parseArgs reports an exit code when parsing stops: 0 after an explicit help
// request, 2 on a usage error, and -1 to continue.
func parseArgs(fs *flag.FlagSet, args []string) int {
	err := fs.Parse(orderFlags(fs, args))
	if err == nil {
		return -1
	}
	path := strings.Fields(fs.Name())
	if errors.Is(err, flag.ErrHelp) {
		printHelp(os.Stdout, path...)
		return 0
	}
	printHelp(os.Stderr, path...)
	return 2
}

// orderFlags moves every flag ahead of the arguments. The standard flag
// package stops parsing at the first non-flag argument, so without this a
// flag written after the arguments is dropped in silence: `bot bind dev-bot
// --force` would pair as if --force were absent. A "--" ends the flags, so
// everything after it stays an argument.
func orderFlags(fs *flag.FlagSet, args []string) []string {
	flags := make([]string, 0, len(args))
	arguments := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			arguments = append(arguments, args[i:]...)
			return append(flags, arguments...)
		case !isFlag(arg):
			arguments = append(arguments, arg)
		default:
			flags = append(flags, arg)
			if takesValue(fs, arg) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	return append(flags, arguments...)
}

// A lone "-" is an argument, by the same convention the flag package
// follows.
func isFlag(arg string) bool {
	return len(arg) > 1 && arg[0] == '-'
}

// takesValue: a flag written --name=value carries its own value, and a
// boolean flag never takes one (IsBoolFlag, the flag package's own
// distinction).
func takesValue(fs *flag.FlagSet, arg string) bool {
	name, _, carriesValue := strings.Cut(arg, "=")
	if carriesValue {
		return false
	}
	defined := fs.Lookup(strings.TrimLeft(name, "-"))
	if defined == nil {
		return false
	}
	boolean, ok := defined.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolean.IsBoolFlag()
}
