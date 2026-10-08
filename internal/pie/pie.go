// Package pie finds kcp APIExports and APIResourceSchemas in a manifest tree and draws them as d2.
package pie

import "fmt"

// Model is the set of kcp API objects found in a manifest tree.
type Model struct {
	Exports []Export
	Schemas []Schema
}

// Export is an APIExport and the resources it serves.
type Export struct {
	// Path is the slash-separated manifest file, relative to the scanned tree, and Doc the
	// document's position in it. Together they identify the export; names repeat across a tree.
	Path      string
	Doc       int
	Name      string
	Resources []Resource
}

// location is where the export is written down, in the path#N form diagnostics use.
func (e Export) location() string { return fmt.Sprintf("%s#%d", e.Path, e.Doc) }

// Resource is one resource an APIExport serves, and the APIResourceSchema that defines it.
type Resource struct {
	Group  string
	Name   string
	Schema string
}

// Schema is an APIResourceSchema and the resource type it defines.
type Schema struct {
	// Path is the slash-separated manifest file, relative to the scanned tree.
	Path   string
	Name   string
	Group  string
	Kind   string
	Plural string
}

// Level says how much a Diagnostic matters to someone reading the diagram.
type Level int

const (
	// Warning means an APIExport or APIResourceSchema is missing from the diagram.
	Warning Level = iota + 1
	// Skipped means a document is not drawn because it is of no interest.
	Skipped
)

var levelNames = map[Level]string{Warning: "warning", Skipped: "skipped"}

func (l Level) String() string { return levelNames[l] }

// Diagnostic is a remark about one document of one manifest file.
type Diagnostic struct {
	Level  Level
	Path   string
	Doc    int
	Reason string
}

func (d Diagnostic) String() string {
	return fmt.Sprintf("%s: %s#%d: %s", d.Level, d.Path, d.Doc, d.Reason)
}
