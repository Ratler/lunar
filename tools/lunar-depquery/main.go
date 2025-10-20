package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/lunar-linux/lunar/tools/lunar-depquery/resolver"
	"github.com/lunar-linux/lunar/tools/lunar-depquery/state"
)

const version = "1.0.0"

func usage() {
	fmt.Fprintf(os.Stderr, `lunar-depquery v%s - Fast dependency query utility for Lunar Linux

Usage: lunar-depquery <command> [arguments]

Commands:
  is-installed <module> [module2...]  Check if module(s) are installed
  is-held <module>                    Check if module is held
  is-exiled <module>                  Check if module is exiled
  is-enforced <module>                Check if module is enforced
  find-deps <module>                  Find all required dependencies recursively
  sort-deps <module1> [module2...]    Sort modules by dependency order
  in-depends <module> <dep>           Check if dep is a dependency of module
  is-depends <dep>                    Check if dep is used by any module
  get-depends <module>                Get all dependencies for module
  version                             Show version information

Exit codes:
  0 - Success (or condition is true)
  1 - Failure (or condition is false)
  2 - Usage error

State files read from environment or defaults:
  MODULE_STATUS   (default: /var/state/lunar/packages)
  DEPENDS_STATUS  (default: /var/state/lunar/depends)
  DEPENDS_CACHE   (default: /var/state/lunar/depends.cache)
  MODULE_INDEX    (default: /var/state/lunar/module.index)
`, version)
	os.Exit(2)
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	command := os.Args[1]

	if command == "version" {
		fmt.Printf("lunar-depquery v%s\n", version)
		os.Exit(0)
	}

	if command == "help" || command == "-h" || command == "--help" {
		usage()
	}

	// Load state files
	moduleStatusPath := getEnvOrDefault("MODULE_STATUS", "/var/state/lunar/packages")
	dependsStatusPath := getEnvOrDefault("DEPENDS_STATUS", "/var/state/lunar/depends")
	dependsCachePath := getEnvOrDefault("DEPENDS_CACHE", "/var/state/lunar/depends.cache")

	loader := state.NewLoader()
	if err := loader.Load(moduleStatusPath, dependsStatusPath, dependsCachePath); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading state files: %v\n", err)
		os.Exit(1)
	}

	r := resolver.New(loader)

	switch command {
	case "is-installed":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Error: is-installed requires at least one module name\n")
			os.Exit(2)
		}
		// Support multiple modules - print installed ones
		if len(os.Args) > 3 {
			var installed []string
			for _, module := range os.Args[2:] {
				if r.IsInstalled(module) {
					installed = append(installed, module)
				}
			}
			if len(installed) > 0 {
				fmt.Println(strings.Join(installed, " "))
				os.Exit(0)
			}
			os.Exit(1)
		}
		// Single module - exit code only
		if r.IsInstalled(os.Args[2]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "is-held":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: is-held requires exactly one module name\n")
			os.Exit(2)
		}
		if r.IsHeld(os.Args[2]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "is-exiled":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: is-exiled requires exactly one module name\n")
			os.Exit(2)
		}
		if r.IsExiled(os.Args[2]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "is-enforced":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: is-enforced requires exactly one module name\n")
			os.Exit(2)
		}
		if r.IsEnforced(os.Args[2]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "find-deps":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: find-deps requires exactly one module name\n")
			os.Exit(2)
		}
		deps, err := r.FindDependencies(os.Args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error finding dependencies: %v\n", err)
			os.Exit(1)
		}
		for _, dep := range deps {
			fmt.Println(dep)
		}
		os.Exit(0)

	case "sort-deps":
		if len(os.Args) < 3 {
			fmt.Fprintf(os.Stderr, "Error: sort-deps requires at least one module name\n")
			os.Exit(2)
		}
		sorted, err := r.SortByDependency(os.Args[2:])
		// Always output the partial result (matches tsort behavior)
		for _, module := range sorted {
			fmt.Println(module)
		}
		// Exit with error code if there was a cycle, but after outputting partial result
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error sorting dependencies: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)

	case "in-depends":
		if len(os.Args) != 4 {
			fmt.Fprintf(os.Stderr, "Error: in-depends requires module and dependency names\n")
			os.Exit(2)
		}
		if r.InDepends(os.Args[2], os.Args[3]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "is-depends":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: is-depends requires exactly one module name\n")
			os.Exit(2)
		}
		if r.IsDepends(os.Args[2]) {
			os.Exit(0)
		}
		os.Exit(1)

	case "get-depends":
		if len(os.Args) != 3 {
			fmt.Fprintf(os.Stderr, "Error: get-depends requires exactly one module name\n")
			os.Exit(2)
		}
		deps := r.GetDepends(os.Args[2])
		for _, dep := range deps {
			fmt.Printf("%s:%s:%s:%s:%s:%s\n",
				dep.Module, dep.Dep, dep.Status, dep.Type, dep.OnOpts, dep.OffOpts)
		}
		os.Exit(0)

	default:
		fmt.Fprintf(os.Stderr, "Error: unknown command '%s'\n", command)
		usage()
	}
}
