package pie_test

import (
	"strings"
	"testing"

	"github.com/bmarinov/apiexport-pie/internal/pie"
)

func coreExportAt(path string) pie.Export {
	return pie.Export{Path: path, Doc: 1, Name: "core.example.io", Resources: []pie.Resource{
		{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"},
		{Group: "core.example.io", Name: "gadgets", Schema: "v1.gadgets.core.example.io"},
	}}
}

func widgetSchemaAt(path string) pie.Schema {
	return pie.Schema{Path: path, Name: "v1.widgets.core.example.io", Group: "core.example.io", Kind: "Widget", Plural: "widgets"}
}

func gadgetSchemaAt(path string) pie.Schema {
	return pie.Schema{Path: path, Name: "v1.gadgets.core.example.io", Group: "core.example.io", Kind: "Gadget", Plural: "gadgets"}
}

const elkLayout = `vars: {
  d2-config: {
    layout-engine: elk
  }
}
`

// legend is the box a diagram ends with, drawing one example edge per relation it shows.
func legend(entries ...string) string {
	return `"legend": {
  label: "Legend"
  near: bottom-left
  style.stroke-dash: 2
  style.fill: transparent
  *.style.font-size: 12
` + strings.Join(entries, "") + "}\n"
}

const (
	legendBindsByDefault = `  "default:from": "WorkspaceType"
  "default:to": "APIExport"
  "default:from" -> "default:to": "binds by default" {style.stroke: "#1971c2"}
`
	legendBindsThroughExtend = `  "extend:from": "WorkspaceType"
  "extend:to": "APIExport"
  "extend:from" -> "extend:to": "binds through extend" {style.stroke: "#1971c2"; style.stroke-dash: 2}
`
	legendBindsByAPIBinding = `  "apibinding:from": "APIBinding file"
  "apibinding:to": "APIExport"
  "apibinding:from" -> "apibinding:to": "binds" {style.stroke: "#2f9e44"}
`
	legendBindsRefused = `  "refused:from": "consumer"
  "refused:to": "APIExport"
  "refused:from" -> "refused:to": "conflicts" {style.stroke: "#e03131"; style.stroke-width: 3}
`
	legendShared = `  "shared": "APIExport with a resource another export also serves" {style.fill: "#f08c00"}
`
	legendClaims = `  "claims:from": "APIExport"
  "claims:to": "claimed kind"
  "claims:from" -> "claims:to": "claims" {style.stroke: "#7048e8"; style.stroke-dash: 4}
`
)

func withClaims(e pie.Export, claims ...pie.Claim) pie.Export {
	e.Claims = claims
	return e
}

func widgetResource() pie.Resource {
	return pie.Resource{Group: "core.example.io", Name: "widgets", Schema: "v1.widgets.core.example.io"}
}

func gadgetResource() pie.Resource {
	return pie.Resource{Group: "core.example.io", Name: "gadgets", Schema: "v1.gadgets.core.example.io"}
}

func widgetSchemaKindAt(path, kind string) pie.Schema {
	s := widgetSchemaAt(path)
	s.Kind = kind
	return s
}

func withV1alpha1(e pie.Export) pie.Export {
	e.V1alpha1 = true
	return e
}

const overviewHead = `vars: {
  d2-config: {
    layout-engine: dagre
  }
}
direction: left
classes: {
  column: {style: {fill: transparent; stroke: transparent; font-size: 22; bold: true; font-color: "#868e96"}}
  group: {style: {fill: "#f1f3f5"; stroke: "#ced4da"; font-size: 15; border-radius: 6}}
  kind: {style: {fill: "#ffffff"; stroke: "#adb5bd"; font-size: 14; border-radius: 4}}
  export: {style: {fill: "#ffffff"; stroke: "#364fc7"; stroke-width: 2; font-size: 16; bold: true; border-radius: 6}}
  story: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: false; font-color: "#343a40"}}
  warning: {style: {fill: transparent; stroke: transparent; font-size: 13; bold: true; font-color: "#e8590c"}}
  workspace: {style: {fill: "#f8f9fa"; stroke: "#868e96"; font-size: 15; border-radius: 8}}
  workspacetype: {style: {fill: "#e7f5ff"; stroke: "#1971c2"; font-size: 15; border-radius: 6}}
  apibindings: {style: {fill: "#ebfbee"; stroke: "#2f9e44"; font-size: 15; border-radius: 6}}
  claimed: {style: {fill: transparent; stroke: "#adb5bd"; stroke-dash: 3; font-size: 15; border-radius: 6}}
  claimed-resource: {style: {fill: "#f8f9fa"; stroke: "#adb5bd"; stroke-dash: 3; font-size: 14; border-radius: 4; font-color: "#495057"}}
}
`

// overviewLegend is the legend under the overview, where it adds no width.
func overviewLegend(entries ...string) string {
	return strings.Replace(legend(entries...), "near: bottom-left", "near: bottom-center", 1)
}

const (
	legendServes = `  "serves:from": "APIExport"
  "serves:to": "kind"
  "serves:from" -> "serves:to": "serves" {style.stroke: "#868e96"}
`
	legendSharedKind = `  "shared": "kind more than one APIExport serves" {style.fill: "#f08c00"}
`
	legendAPIGroup = `  "api-group": {
    label: "API group"
    class: group
    "kind": "kind" {class: kind}
  }
`
	legendClaimsRead = `  "reads:from": "APIExport"
  "reads:to": "what it claims"
  "reads:from" -> "reads:to": "claims to read, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4}
`
	legendClaimsWrite = `  "writes:from": "APIExport"
  "writes:to": "what it claims"
  "writes:from" -> "writes:to": "claims to write, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 4}
`
	legendClaimsAll = `  "all-verbs:from": "APIExport"
  "all-verbs:to": "what it claims"
  "all-verbs:from" -> "all-verbs:to": "claims all verbs, through its virtual workspace" {style.stroke: "#7048e8"; style.stroke-dash: 4; style.stroke-width: 6}
`
)

// wantBlocks fails unless got holds every block, each verbatim and on whole lines. A case lists
// the boxes and edges it is about; the rest of the diagram is pinned by its renderer's
// whole-diagram cases.
func wantBlocks(t *testing.T, got string, blocks ...string) {
	t.Helper()
	var missing []string
	for _, block := range blocks {
		if !strings.Contains("\n"+got, "\n"+strings.TrimSuffix(block, "\n")+"\n") {
			missing = append(missing, strings.TrimSuffix(block, "\n"))
		}
	}
	if len(missing) > 0 {
		t.Errorf("output lacks:\n%s\n\noutput:\n%s", strings.Join(missing, "\n---\n"), got)
	}
}
