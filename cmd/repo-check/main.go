package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/matteoroda05/repo-check/internal/check"
	"github.com/matteoroda05/repo-check/internal/config"
	"github.com/matteoroda05/repo-check/internal/gitrepo"
)

const usage = `Usage
  repo-check add PATH             Register a repository
  repo-check add --folder PATH    Register a folder of repositories
  repo-check [check]              Fetch and check all registered repositories
  repo-check check [REPO ...]     Check selected repositories

Select by path or unique folder name. Use check --jobs N to set concurrency.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string) int {
	command := "check"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}
	switch command {
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	case "add", "check":
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q\n\n%s", command, usage)
		return 2
	}

	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(flags.Output(), usage) }
	var folder bool
	var workers int
	if command == "add" {
		flags.BoolVar(&folder, "folder", false, "Register a parent folder")
	} else {
		flags.IntVar(&workers, "jobs", 4, "Maximum concurrent repository checks")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	path := os.Getenv("REPO_CHECK_CONFIG")
	if path == "" {
		var err error
		path, err = config.Path()
		if err != nil {
			return fail(err)
		}
	}
	registry, err := config.Load(path)
	if err != nil {
		return fail(fmt.Errorf("read configuration %s: %w", path, err))
	}
	if command == "add" {
		if flags.NArg() != 1 {
			flags.Usage()
			return 2
		}
		return register(registry, path, flags.Arg(0), folder)
	}
	if workers <= 0 {
		fmt.Fprintln(os.Stderr, "--jobs must be at least 1")
		return 2
	}
	return runCheck(ctx, registry, flags.Args(), workers)
}

func register(registry config.Registry, configPath, input string, folder bool) int {
	path, err := config.CanonicalPath(input)
	if err != nil {
		return fail(err)
	}
	if folder {
		info, err := os.Stat(path)
		if err != nil {
			return fail(err)
		}
		if !info.IsDir() {
			return fail(fmt.Errorf("%s is not a directory", path))
		}
	} else if _, err := gitrepo.Open(path); err != nil {
		return fail(fmt.Errorf("open repository %s: %w", path, err))
	}
	registry.Add(path, folder)
	if err := registry.Save(configPath); err != nil {
		return fail(fmt.Errorf("save configuration %s: %w", configPath, err))
	}
	fmt.Printf("Registered %s\n", path)
	return 0
}

func runCheck(ctx context.Context, registry config.Registry, selectors []string, workers int) int {
	paths, scanErr := check.Discover(registry)
	code := 0
	if scanErr != nil {
		code = fail(scanErr)
	}
	paths, err := check.Select(paths, selectors)
	if err != nil {
		return fail(err)
	}
	if len(paths) == 0 {
		fmt.Println("No repositories found. Register one with repo-check add PATH.")
	}
	for _, result := range check.Run(ctx, paths, workers) {
		if !printResult(result) {
			code = 1
		}
	}
	return code
}

func printResult(result check.Result) bool {
	fmt.Println(result.Path)
	status := result.Status
	if status.Err != nil {
		fmt.Printf("  Error: %v\n\n", status.Err)
		return false
	}
	fmt.Printf("  HEAD %s\n", status.Head)
	if status.Note != "" {
		fmt.Printf("  %s\n", status.Note)
	}
	if len(status.Remotes) == 0 {
		fmt.Println("  No configured remotes")
	}
	ok := true
	for _, item := range status.Local {
		if !printComparison(item) {
			ok = false
		}
	}
	for _, remote := range status.Remotes {
		fmt.Printf("  %s\n", remote.Name)
		if remote.Err != nil {
			fmt.Printf("    Error: %v\n", remote.Err)
			ok = false
		}
		for _, item := range remote.Comparisons {
			if !printComparison(item) {
				ok = false
			}
		}
	}
	fmt.Println()
	return ok
}

func printComparison(item gitrepo.Comparison) bool {
	status := string(item.Relationship)
	if item.Kind == "Default branch" {
		switch item.Relationship {
		case gitrepo.Current, gitrepo.Ahead:
			status = "contains all commits"
		case gitrepo.Behind:
			status = "missing commits"
		case gitrepo.Diverged:
			status = "missing commits; " + status
		}
	}
	if item.Err != nil {
		status += " (" + item.Err.Error() + ")"
	}
	if item.Tracking {
		status += " [tracking target]"
	}
	fmt.Printf("    %-16s %s  %s\n", item.Kind, item.Target, status)
	return item.Err == nil
}

func fail(err error) int {
	fmt.Fprintln(os.Stderr, err)
	return 1
}
