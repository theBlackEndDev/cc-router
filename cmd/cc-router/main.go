// Command cc-router is the single executable behind every cc-router hook,
// skill and statusline call.
//
//	cc-router hook <session-start|pre-agent|post-agent>   (stdin: hook JSON)
//	cc-router status  [--data DIR]
//	cc-router doctor  [--data DIR]
//	cc-router version
//
// Hooks are fail-open: any error is logged to <data>/errors.log and the
// process exits 0 with no output, so Claude Code keeps working.
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/theblackenddev/cc-router/internal/router"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	args := os.Args[1:]
	dataDir := os.Getenv("CLAUDE_PLUGIN_DATA")
	var rest []string
	for i := 0; i < len(args); i++ {
		if args[i] == "--data" && i+1 < len(args) {
			dataDir = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	env := router.Env{Getenv: os.Getenv, DataDir: dataDir, Now: time.Now}

	if len(rest) == 0 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "hook":
		if len(rest) < 2 {
			os.Exit(0)
		}
		runHook(rest[1], env)
	case "status":
		router.Status(os.Stdout, env)
	case "doctor":
		if !router.Doctor(os.Stdout, env, version) {
			os.Exit(1)
		}
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
}

func runHook(event string, env router.Env) {
	defer func() {
		if r := recover(); r != nil {
			logError(env, fmt.Errorf("panic in %s: %v", event, r))
		}
		os.Exit(0)
	}()
	out, err := router.Run(event, os.Stdin, env)
	if err != nil {
		logError(env, err)
	}
	if len(out) > 0 {
		os.Stdout.Write(out)
	}
}

func logError(env router.Env, err error) {
	_ = router.AppendLog(env.DataDir, "errors.log", map[string]any{"error": err.Error(), "version": version})
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: cc-router hook <event> | status | doctor | version  [--data DIR]")
}
