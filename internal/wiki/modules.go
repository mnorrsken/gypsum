package wiki

import (
	"fmt"
	"slices"
	"strings"
)

// Module is an optional feature area of the wiki (a section in the sidebar
// menu, page sharing or the MCP endpoint) that can be turned on or off. Pages,
// search, history and recent edits are the core and always on.
type Module string

const (
	ModuleNotes   Module = "notes"   // quick notes board and the notes MCP tools (deprecated)
	ModuleFiles   Module = "files"   // file manager, uploads and the file MCP tools
	ModuleSecrets Module = "secrets" // secrets vault
	ModuleGraph   Module = "graph"   // link graph view
	ModuleSkills  Module = "skills"  // skills and the skills MCP tools
	ModuleScan    Module = "scan"    // photo scanning (also needs ANTHROPIC_API_KEY)
	ModuleSharing Module = "sharing" // public share links for pages and skills
	ModuleMCP     Module = "mcp"     // MCP endpoint and its OAuth routes
)

// AllModules lists every module in menu order.
var AllModules = []Module{ModuleNotes, ModuleFiles, ModuleSecrets, ModuleGraph, ModuleSkills, ModuleScan, ModuleSharing, ModuleMCP}

// DefaultModules is the set used when GYPSUM_MODULES is unset: everything
// except quick notes, which are deprecated.
const DefaultModules = "files,secrets,graph,skills,scan,sharing,mcp"

// ModuleSet is the set of enabled modules. A nil set enables every module, so
// code that never configures modules keeps all features.
type ModuleSet map[Module]bool

// Has reports whether module m is enabled.
func (s ModuleSet) Has(m Module) bool {
	return s == nil || s[m]
}

// ParseModules parses a comma-separated list of module names, e.g.
// "files,skills". An empty string gives DefaultModules and "none" disables
// every module. Unknown names are an error so a typo does not silently turn
// a feature off.
func ParseModules(s string) (ModuleSet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = DefaultModules
	}
	set := ModuleSet{}
	if strings.EqualFold(s, "none") {
		return set, nil
	}
	for part := range strings.SplitSeq(s, ",") {
		name := Module(strings.ToLower(strings.TrimSpace(part)))
		if name == "" {
			continue
		}
		if !slices.Contains(AllModules, name) {
			return nil, fmt.Errorf("unknown module %q (known: %s)", name, moduleNames())
		}
		set[name] = true
	}
	return set, nil
}

func moduleNames() string {
	names := make([]string, len(AllModules))
	for i, m := range AllModules {
		names[i] = string(m)
	}
	return strings.Join(names, ", ")
}
