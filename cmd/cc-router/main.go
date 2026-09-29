// Command cc-router is the single executable behind every cc-router hook,
// skill and statusline call.
//
//	cc-router hook <event>              (stdin: hook JSON)
//	cc-router statusline                (stdin: statusline JSON)
//	cc-router setup [--root DIR] [--remove]
//	cc-router status | doctor | version
//
// Every command accepts --data DIR (default $CLAUDE_PLUGIN_DATA).
// Hooks and the statusline are fail-open: errors go to <data>/errors.log and
// the process exits 0, so Claude Code keeps working. The one deliberate
// exception is a hook block, which exits 2 with the reason on stderr.
package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/theblackenddev/cc-router/internal/router"
)

var version = "dev" // set with -ldflags "-X main.version=..."

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, time.Now))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv router.Getenv, now func() time.Time) int {
	dataDir := getenv("CLAUDE_PLUGIN_DATA")
	root := getenv("CLAUDE_PLUGIN_ROOT")
	remove := false
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--data" && i+1 < len(args):
			dataDir = args[i+1]
			i++
		case args[i] == "--root" && i+1 < len(args):
			root = args[i+1]
			i++
		case args[i] == "--remove":
			remove = true
		default:
			rest = append(rest, args[i])
		}
	}
	env := router.Env{Getenv: getenv, DataDir: dataDir, Now: now}

	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: cc-router hook <event> | statusline | setup [--remove] | status | doctor | version  [--data DIR]")
		return 2
	}
	switch rest[0] {
	case "hook":
		if len(rest) < 2 {
			return 0
		}
		return runHook(rest[1], stdin, stdout, stderr, env)
	case "statusline":
		return failOpen(env, "statusline", func() error { return router.Statusline(stdin, stdout, env) })
	case "setup":
		var err error
		if remove {
			err = router.Remove(stdout, env)
		} else {
			err = router.Setup(stdout, env, root)
		}
		if err != nil {
			fmt.Fprintln(stderr, "cc-router setup:", err)
			return 1
		}
	case "status":
		router.Status(stdout, env)
	case "doctor":
		if !router.Doctor(stdout, env, version) {
			return 1
		}
	case "version":
		fmt.Fprintln(stdout, version)
	default:
		fmt.Fprintf(stderr, "cc-router: unknown command %q\n", rest[0])
		return 2
	}
	return 0
}

func runHook(event string, stdin io.Reader, stdout, stderr io.Writer, env router.Env) (code int) {
	var res router.Result
	raw, _ := io.ReadAll(stdin)
	code = failOpen(env, "hook "+event, func() error {
		var err error
		res, err = router.Run(event, bytes.NewReader(raw), env)
		return err
	})
	if env.Getenv("CC_ROUTER_DEBUG") != "" {
		in := string(raw)
		if len(in) > 4000 {
			in = in[:4000] + "…"
		}
		_ = router.AppendLog(env.DataDir, "debug.log", map[string]any{
			"hook": event, "input": in, "stdout": string(res.Stdout), "block": res.Block,
		})
	}
	if res.Block != "" {
		fmt.Fprintln(stderr, res.Block)
		return 2
	}
	stdout.Write(res.Stdout)
	return code
}

// failOpen runs f, logs any error or panic, and always reports success.
func failOpen(env router.Env, what string, f func() error) int {
	defer func() {
		if r := recover(); r != nil {
			logError(env, fmt.Errorf("panic in %s: %v", what, r))
		}
	}()
	if err := f(); err != nil {
		logError(env, fmt.Errorf("%s: %w", what, err))
	}
	return 0
}

func logError(env router.Env, err error) {
	_ = router.AppendLog(env.DataDir, "errors.log", map[string]any{"error": err.Error(), "version": version})
}
