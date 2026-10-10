package pie

import (
	"bytes"
	"fmt"
	"path"
	"slices"
	"strings"
)

// RenderDiff lists what changes from a to b, a line per change, in the order of each tree. Exports
// are told apart by name, so one that moved to another file is no change and a renamed one is
// removed and added. Kinds are told apart by resource and follow it from export to export.
func RenderDiff(a, b Model) []byte {
	var out bytes.Buffer
	before, after := exportNames(a.Exports), exportNames(b.Exports)
	for _, name := range before {
		if !slices.Contains(after, name) {
			fmt.Fprintf(&out, "removed: APIExport %s\n", name)
		}
	}
	for _, name := range after {
		if !slices.Contains(before, name) {
			fmt.Fprintf(&out, "added: APIExport %s\n", name)
		}
	}

	servedBefore, servedAfter := exportsServing(a.Exports), exportsServing(b.Exports)
	for _, resource := range resourcesOf(a.Exports, b.Exports) {
		left := without(servedBefore[resource], servedAfter[resource])
		came := without(servedAfter[resource], servedBefore[resource])
		kind := kindNamed(resource, b)
		if len(came) == 0 {
			kind = kindNamed(resource, a)
		}
		if len(left) > 0 && len(came) > 0 {
			fmt.Fprintf(&out, "moved: kind %s from %s to %s\n", kind, strings.Join(left, " and "), strings.Join(came, " and "))
			continue
		}
		for _, e := range left {
			fmt.Fprintf(&out, "removed: kind %s from %s\n", kind, e)
		}
		for _, e := range came {
			fmt.Fprintf(&out, "added: kind %s in %s\n", kind, e)
		}
	}
	return out.Bytes()
}

// resourcesOf lists the resources the exports of both trees serve, those of the first tree first.
func resourcesOf(trees ...[]Export) []string {
	var resources []string
	for _, exports := range trees {
		for _, e := range exports {
			for _, r := range e.Resources {
				if name := resourceName(r); !slices.Contains(resources, name) {
					resources = append(resources, name)
				}
			}
		}
	}
	return resources
}

// kindNamed names a resource of a tree as Kind.group, the way Kubernetes writes Deployment.apps,
// or by the resource itself when its schema is not in the tree.
func kindNamed(resource string, m Model) string {
	for _, e := range m.Exports {
		for _, r := range e.Resources {
			if resourceName(r) != resource {
				continue
			}
			if s, ok := schemaOf(r, path.Dir(e.Path), m.Schemas); ok {
				return s.Kind + "." + r.Group
			}
			return resource
		}
	}
	return resource
}

func without(names, others []string) []string {
	var rest []string
	for _, name := range names {
		if !slices.Contains(others, name) {
			rest = append(rest, name)
		}
	}
	return rest
}

// exportNames lists the names of exports once each, copies of an export sharing its name.
func exportNames(exports []Export) []string {
	var names []string
	for _, e := range exports {
		if !slices.Contains(names, e.Name) {
			names = append(names, e.Name)
		}
	}
	return names
}
