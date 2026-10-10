// Package pie finds kcp APIExports and APIResourceSchemas in a manifest tree and draws them as d2.
package pie

import "fmt"

// Model is the set of kcp API objects found in a manifest tree.
type Model struct {
	Exports        []Export
	Schemas        []Schema
	WorkspaceTypes []WorkspaceType
	Workspaces     []Workspace
	APIBindings    []APIBinding
}

// Export is an APIExport and the resources it serves.
type Export struct {
	// Path is the slash-separated manifest file, relative to the scanned tree, and Doc the
	// document's position in it. Together they identify the export; names repeat across a tree.
	Path      string
	Doc       int
	Name      string
	Resources []Resource
	Claims    []Claim
	// IdentitySecret says the export names the secret its identity comes from. Without one kcp
	// generates the identity, a new one in every installation, and with it the hash claims name.
	IdentitySecret bool
}

// Claim is a permission claim: the export's provider asks every consumer for access to Resource
// in Group, with these Verbs.
type Claim struct {
	Group    string
	Resource string
	Verbs    []string
	// Selected says a default selector narrows the claim to some objects of the resource.
	Selected bool
	// FromExport says the claim names an APIExport's identity hash: the resource is served by that
	// export, so only workspaces binding it have it. Without one, it is built into every workspace.
	FromExport bool
	// LiteralHash says the identity hash is written out in the manifest, not templated in when it
	// is installed.
	LiteralHash bool
}

// GroupResource names a resource by its group, empty for the core group, and its plural.
type GroupResource struct {
	Group    string
	Resource string
}

// location is where an object is written down, in the path#N form diagnostics use.
func location(path string, doc int) string { return fmt.Sprintf("%s#%d", path, doc) }

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
	// Singular, ShortNames and ListKind are what else kcp compares before it binds two resources of
	// one group into a workspace.
	Singular   string
	ShortNames []string
	ListKind   string
}

// Level says how much a Diagnostic matters to someone reading the diagram.
type Level int

const (
	// Warning means an APIExport or APIResourceSchema is missing from the diagram.
	Warning Level = iota + 1
	// Skipped means a document is not drawn because it is of no interest.
	Skipped
	// Notice means something kcp allows but a designer may want to look at: a fact, not a fault.
	Notice
)

var levelNames = map[Level]string{Warning: "warning", Skipped: "skipped", Notice: "notice"}

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

// WorkspaceType is a use case: every workspace of this type binds the exports in Binds, and those
// of the types it Extends.
type WorkspaceType struct {
	// Path is the slash-separated manifest file, relative to the scanned tree, and Doc the
	// document's position in it.
	Path    string
	Doc     int
	Name    string
	Binds   []ExportRef
	Extends []TypeRef
	// Parents and Children are the types limitAllowedParents and limitAllowedChildren list: the
	// workspaces of this type may sit only in those of Parents and hold only those of Children.
	// Empty is no list, whether that is any type or none.
	Parents  []TypeRef
	Children []TypeRef
}

// ExportRef names an APIExport and the workspace path it lives in. An empty Path is the workspace
// of the WorkspaceType that refers to it.
type ExportRef struct {
	Path string
	Name string
}

// Workspace is a workspace the tree creates. Type names its WorkspaceType; an empty name means kcp's
// default. Where the workspace is created is not in the document.
type Workspace struct {
	Path string
	Doc  int
	Name string
	Type TypeRef
}

// TypeRef names a WorkspaceType and the workspace path it lives in.
type TypeRef struct {
	Path string
	Name string
}

// APIBinding is a binding written down as a manifest: whatever workspace it is applied to binds
// Export.
type APIBinding struct {
	// Path is the slash-separated manifest file, relative to the scanned tree, and Doc the
	// document's position in it.
	Path   string
	Doc    int
	Name   string
	Export ExportRef
	// Accepts lists the export's claims the binding accepts; kcp grants it no others.
	Accepts []GroupResource
}
