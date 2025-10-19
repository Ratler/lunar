package resolver

import (
	"fmt"

	"github.com/lunar-linux/lunar/tools/lunar-depquery/state"
)

// Resolver provides dependency resolution operations
type Resolver struct {
	loader *state.Loader
}

// New creates a new resolver
func New(loader *state.Loader) *Resolver {
	return &Resolver{loader: loader}
}

// IsInstalled checks if a module is installed (or held)
func (r *Resolver) IsInstalled(module string) bool {
	m := r.loader.GetModule(module)
	if m == nil {
		return false
	}
	return m.State == "installed" || m.State == "held"
}

// IsHeld checks if a module is held
func (r *Resolver) IsHeld(module string) bool {
	m := r.loader.GetModule(module)
	if m == nil {
		return false
	}
	return m.State == "held"
}

// IsExiled checks if a module is exiled
func (r *Resolver) IsExiled(module string) bool {
	m := r.loader.GetModule(module)
	if m == nil {
		return false
	}
	return m.State == "exiled"
}

// IsEnforced checks if a module is enforced
func (r *Resolver) IsEnforced(module string) bool {
	m := r.loader.GetModule(module)
	if m == nil {
		return false
	}
	return m.State == "enforced"
}

// InDepends checks if dep is a dependency of module with status=on
func (r *Resolver) InDepends(module, dep string) bool {
	deps := r.loader.GetDependencies(module)
	for _, d := range deps {
		if d.Dep == dep && d.Status == "on" {
			return true
		}
	}
	return false
}

// IsDepends checks if dep is used by any module (status=on)
func (r *Resolver) IsDepends(dep string) bool {
	return r.loader.IsDepUsed(dep)
}

// GetDepends returns all dependencies for a module
func (r *Resolver) GetDepends(module string) []*state.Dependency {
	return r.loader.GetDependencies(module)
}

// FindDependencies recursively finds all required dependencies for a module
// This mimics the find_depends() function from depends.lunar
func (r *Resolver) FindDependencies(module string) ([]string, error) {
	visited := make(map[string]bool)
	var result []string

	var findDepsRecursive func(string) error
	findDepsRecursive = func(mod string) error {
		if visited[mod] {
			return nil
		}
		visited[mod] = true

		// Get dependencies from cache first
		cachedDeps := r.loader.GetCachedDeps(mod)
		if len(cachedDeps) == 0 {
			// If not in cache, try DEPENDS_STATUS for zlocal modules
			deps := r.loader.GetDependencies(mod)
			for _, dep := range deps {
				if dep.Type == "required" {
					result = append(result, dep.Dep)
					if err := findDepsRecursive(dep.Dep); err != nil {
						return err
					}
				}
			}
			return nil
		}

		// Process cached dependencies
		for _, cached := range cachedDeps {
			if cached.Type == "required" {
				result = append(result, cached.Dep)
				if err := findDepsRecursive(cached.Dep); err != nil {
					return err
				}
			}
		}

		return nil
	}

	if err := findDepsRecursive(module); err != nil {
		return nil, err
	}

	return result, nil
}

// SortByDependency performs topological sort on modules by dependency
// This mimics the sort_by_dependency() function from depends.lunar
func (r *Resolver) SortByDependency(modules []string) ([]string, error) {
	// Build adjacency list from DEPENDS_STATUS where status="on"
	graph := make(map[string][]string)
	inDegree := make(map[string]int)

	// Initialize all input modules
	moduleSet := make(map[string]bool)
	for _, mod := range modules {
		moduleSet[mod] = true
		inDegree[mod] = 0
	}

	// Build graph from all dependencies with status=on
	for module := range r.loader.Dependencies {
		deps := r.loader.GetDependencies(module)
		for _, dep := range deps {
			if dep.Status == "on" {
				// Add edge: dep -> module (module depends on dep)
				if !contains(graph[dep.Dep], module) {
					graph[dep.Dep] = append(graph[dep.Dep], module)
				}

				// Only count in-degree if both nodes are in our module set
				if moduleSet[module] && moduleSet[dep.Dep] {
					inDegree[module]++
					// Ensure dep is in inDegree map
					if _, exists := inDegree[dep.Dep]; !exists {
						inDegree[dep.Dep] = 0
					}
				}
			}
		}
	}

	// Kahn's algorithm for topological sort
	var queue []string
	for _, mod := range modules {
		if inDegree[mod] == 0 {
			queue = append(queue, mod)
		}
	}

	var result []string
	for len(queue) > 0 {
		// Pop from queue
		current := queue[0]
		queue = queue[1:]
		result = append(result, current)

		// Reduce in-degree of neighbors
		for _, neighbor := range graph[current] {
			if !moduleSet[neighbor] {
				continue
			}
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	// Add any modules not in the result (no dependencies)
	resultSet := make(map[string]bool)
	for _, mod := range result {
		resultSet[mod] = true
	}
	for _, mod := range modules {
		if !resultSet[mod] {
			result = append(result, mod)
		}
	}

	// Check for cycles (if result doesn't contain all modules)
	if len(result) < len(modules) {
		return result, fmt.Errorf("dependency cycle detected")
	}

	return result, nil
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
