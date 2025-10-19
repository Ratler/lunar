package state

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ModuleState represents a module's installation state
type ModuleState struct {
	Name      string
	Timestamp string
	State     string // "installed", "held", "exiled", "enforced"
	Version   string
	Modifiers string
}

// Dependency represents a dependency relationship
type Dependency struct {
	Module  string
	Dep     string
	Status  string // "on", "off"
	Type    string // "required", "optional"
	OnOpts  string
	OffOpts string
}

// CachedDep represents a cached dependency from moonbase
type CachedDep struct {
	Module      string
	Dep         string
	Type        string
	OnOpts      string
	OffOpts     string
	Description string
}

// Loader loads and maintains Lunar state files
type Loader struct {
	Modules     map[string]*ModuleState
	Dependencies map[string][]*Dependency // key: module name
	DepsCache   map[string][]*CachedDep  // key: module name
	DepsIndex   map[string]bool          // quick lookup for is-depends
}

// NewLoader creates a new state loader
func NewLoader() *Loader {
	return &Loader{
		Modules:      make(map[string]*ModuleState),
		Dependencies: make(map[string][]*Dependency),
		DepsCache:    make(map[string][]*CachedDep),
		DepsIndex:    make(map[string]bool),
	}
}

// Load reads all state files into memory
func (l *Loader) Load(moduleStatusPath, dependsStatusPath, dependsCachePath string) error {
	// Load MODULE_STATUS
	if err := l.loadModuleStatus(moduleStatusPath); err != nil {
		return fmt.Errorf("loading module status: %w", err)
	}

	// Load DEPENDS_STATUS
	if err := l.loadDependsStatus(dependsStatusPath); err != nil {
		return fmt.Errorf("loading depends status: %w", err)
	}

	// Load DEPENDS_CACHE
	if err := l.loadDependsCache(dependsCachePath); err != nil {
		// Non-fatal if cache doesn't exist yet
		if !os.IsNotExist(err) {
			return fmt.Errorf("loading depends cache: %w", err)
		}
	}

	return nil
}

// loadModuleStatus parses MODULE_STATUS file
// Format: module:timestamp:modifiers:state:version:
func (l *Loader) loadModuleStatus(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty state is valid
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue // Skip malformed lines
		}

		module := &ModuleState{
			Name:      parts[0],
			Timestamp: parts[1],
			Modifiers: "",
			State:     "",
			Version:   "",
		}

		// Parse modifiers and state (parts[2] and parts[3] can contain +modifiers)
		// Format examples:
		//   name:timestamp::installed:version:
		//   name:timestamp:+modifier:installed:version:
		//   name:timestamp::held+modifier:version:

		// parts[2] is modifiers field or empty
		if len(parts) > 2 {
			module.Modifiers = parts[2]
		}

		// parts[3] is state field (may contain modifiers after +)
		if len(parts) > 3 {
			stateField := parts[3]
			// Split on + to separate state from modifiers
			stateParts := strings.Split(stateField, "+")
			module.State = stateParts[0]
		}

		// parts[4] is version
		if len(parts) > 4 {
			module.Version = parts[4]
		}

		l.Modules[module.Name] = module
	}

	return scanner.Err()
}

// loadDependsStatus parses DEPENDS_STATUS file
// Format: module:dep:status:type:on_opts:off_opts
func (l *Loader) loadDependsStatus(path string) error {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Empty state is valid
			return nil
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 4 {
			continue // Skip malformed lines
		}

		dep := &Dependency{
			Module:  parts[0],
			Dep:     parts[1],
			Status:  parts[2],
			Type:    parts[3],
			OnOpts:  "",
			OffOpts: "",
		}

		if len(parts) > 4 {
			dep.OnOpts = parts[4]
		}
		if len(parts) > 5 {
			dep.OffOpts = parts[5]
		}

		l.Dependencies[dep.Module] = append(l.Dependencies[dep.Module], dep)

		// Build index for is-depends queries
		if dep.Status == "on" {
			l.DepsIndex[dep.Dep] = true
		}
	}

	return scanner.Err()
}

// loadDependsCache parses DEPENDS_CACHE file
// Format: module:dep:type:on_opts:off_opts:description
func (l *Loader) loadDependsCache(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, ":")
		if len(parts) < 3 {
			continue // Skip malformed lines
		}

		cached := &CachedDep{
			Module:      parts[0],
			Dep:         parts[1],
			Type:        parts[2],
			OnOpts:      "",
			OffOpts:     "",
			Description: "",
		}

		if len(parts) > 3 {
			cached.OnOpts = parts[3]
		}
		if len(parts) > 4 {
			cached.OffOpts = parts[4]
		}
		if len(parts) > 5 {
			cached.Description = parts[5]
		}

		l.DepsCache[cached.Module] = append(l.DepsCache[cached.Module], cached)
	}

	return scanner.Err()
}

// GetModule returns a module's state or nil if not found
func (l *Loader) GetModule(name string) *ModuleState {
	return l.Modules[name]
}

// GetDependencies returns all dependencies for a module
func (l *Loader) GetDependencies(module string) []*Dependency {
	return l.Dependencies[module]
}

// GetCachedDeps returns cached dependencies for a module
func (l *Loader) GetCachedDeps(module string) []*CachedDep {
	return l.DepsCache[module]
}

// IsDepUsed checks if a dependency is used by any module (status=on)
func (l *Loader) IsDepUsed(dep string) bool {
	return l.DepsIndex[dep]
}
