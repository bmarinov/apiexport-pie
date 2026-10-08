package pie

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"regexp"
	"strings"

	apisv1alpha1 "github.com/kcp-dev/sdk/apis/apis/v1alpha1"
	apisv1alpha2 "github.com/kcp-dev/sdk/apis/apis/v1alpha2"
	tenancyv1alpha1 "github.com/kcp-dev/sdk/apis/tenancy/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"sigs.k8s.io/yaml"
)

var (
	apiExportKind          = apisv1alpha2.SchemeGroupVersion.WithKind("APIExport")
	apiExportV1alpha1Kind  = apisv1alpha1.SchemeGroupVersion.WithKind("APIExport")
	apiResourceSchemaKind  = apisv1alpha1.SchemeGroupVersion.WithKind("APIResourceSchema")
	workspaceTypeKind      = tenancyv1alpha1.SchemeGroupVersion.WithKind("WorkspaceType")
	apiBindingKind         = apisv1alpha2.SchemeGroupVersion.WithKind("APIBinding")
	apiBindingV1alpha1Kind = apisv1alpha1.SchemeGroupVersion.WithKind("APIBinding")
)

// templateAction matches one Go template action: "{{", anything that is not "}}", then "}}".
const templateAction = `\{\{(?:[^}]|\}[^}])*\}\}`

// Manifests are often Go templates. Rather than render them, which needs values nobody has here,
// actions are replaced with text that keeps the YAML decodable.
var (
	templateLine  = regexp.MustCompile(`(?m)^[ \t]*` + templateAction + `[ \t]*$`)
	templateValue = regexp.MustCompile(`(?m)(:[ \t]+)` + templateAction + `[ \t]*$`)
	templateText  = regexp.MustCompile(templateAction)

	sniffedAPIVersion = regexp.MustCompile(`(?m)^apiVersion:[ \t]*(\S+)`)
	sniffedKind       = regexp.MustCompile(`(?m)^kind:[ \t]*(\S+)`)
)

// Scan reads every YAML manifest under fsys and collects the kcp API objects it finds, with a
// Diagnostic for every document it does not draw. Only failing to read the tree is an error.
func Scan(fsys fs.FS) (Model, []Diagnostic, error) {
	var s scan
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !isYAML(name) {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return fmt.Errorf("reading %s: %w", name, err)
		}
		return s.addFile(name, cleanTemplates(data))
	})
	if err != nil {
		return Model{}, nil, fmt.Errorf("scanning manifests: %w", err)
	}
	return s.model, s.diags, nil
}

func isYAML(name string) bool {
	ext := path.Ext(name)
	return ext == ".yaml" || ext == ".yml"
}

func cleanTemplates(data []byte) []byte {
	data = templateLine.ReplaceAll(data, nil)
	data = templateValue.ReplaceAll(data, []byte("${1}null"))
	return templateText.ReplaceAll(data, []byte("tmpl"))
}

type scan struct {
	model Model
	diags []Diagnostic
}

func (s *scan) addFile(name string, data []byte) error {
	docs := utilyaml.NewYAMLReader(bufio.NewReader(bytes.NewReader(data)))
	for n := 1; ; n++ {
		doc, err := docs.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("splitting %s into documents: %w", name, err)
		}
		s.addDocument(name, n, doc)
	}
}

func (s *scan) addDocument(name string, n int, doc []byte) {
	apiVersion, kind, metaErr := typeMeta(doc)
	switch schema.FromAPIVersionAndKind(apiVersion, kind) {
	case apiExportKind:
		var export apisv1alpha2.APIExport
		if s.decode(name, n, kind, doc, &export) {
			s.model.Exports = append(s.model.Exports, exportFrom(name, n, export))
		}
	case apiExportV1alpha1Kind:
		var old apisv1alpha1.APIExport
		if !s.decode(name, n, kind, doc, &old) {
			return
		}
		// kcp's own conversion, so schema names are read exactly as the server reads them.
		var export apisv1alpha2.APIExport
		if err := apisv1alpha2.Convert_v1alpha1_APIExport_To_v1alpha2_APIExport(&old, &export, nil); err != nil {
			s.report(Warning, name, n, fmt.Sprintf("APIExport does not convert to v1alpha2: %v", err))
			return
		}
		s.model.Exports = append(s.model.Exports, exportFrom(name, n, export))
	case apiResourceSchemaKind:
		var ars apisv1alpha1.APIResourceSchema
		if s.decode(name, n, kind, doc, &ars) {
			s.model.Schemas = append(s.model.Schemas, Schema{
				Path:   name,
				Name:   ars.Name,
				Group:  ars.Spec.Group,
				Kind:   ars.Spec.Names.Kind,
				Plural: ars.Spec.Names.Plural,
			})
		}
	case workspaceTypeKind:
		var wt tenancyv1alpha1.WorkspaceType
		if s.decode(name, n, kind, doc, &wt) {
			s.model.WorkspaceTypes = append(s.model.WorkspaceTypes, workspaceTypeFrom(name, n, wt))
		}
	case apiBindingKind:
		var binding apisv1alpha2.APIBinding
		if s.decode(name, n, kind, doc, &binding) {
			s.addBinding(name, n, binding)
		}
	case apiBindingV1alpha1Kind:
		var old apisv1alpha1.APIBinding
		if !s.decode(name, n, kind, doc, &old) {
			return
		}
		var binding apisv1alpha2.APIBinding
		if err := apisv1alpha2.Convert_v1alpha1_APIBinding_To_v1alpha2_APIBinding(&old, &binding, nil); err != nil {
			s.report(Warning, name, n, fmt.Sprintf("APIBinding does not convert to v1alpha2: %v", err))
			return
		}
		s.addBinding(name, n, binding)
	default:
		reason := describe(apiVersion, kind) + " is not drawn"
		if metaErr != nil {
			reason = fmt.Sprintf("%s does not decode: %v", describe(apiVersion, kind), metaErr)
		}
		s.report(Skipped, name, n, reason)
	}
}

// typeMeta reads apiVersion and kind. When the document is not valid YAML it falls back to matching
// the two top-level lines, so a broken APIExport is still recognised and warned about.
func typeMeta(doc []byte) (apiVersion, kind string, err error) {
	var meta metav1.TypeMeta
	if err := yaml.Unmarshal(doc, &meta); err != nil {
		return sniff(sniffedAPIVersion, doc), sniff(sniffedKind, doc), err
	}
	return meta.APIVersion, meta.Kind, nil
}

func sniff(field *regexp.Regexp, doc []byte) string {
	if m := field.FindSubmatch(doc); m != nil {
		return string(m[1])
	}
	return ""
}

func describe(apiVersion, kind string) string {
	if kind == "" {
		return "document without kind"
	}
	return strings.TrimSpace(apiVersion + " " + kind)
}

// decode fills obj from doc, or warns why the object cannot be drawn.
func (s *scan) decode(name string, n int, kind string, doc []byte, obj metav1.Object) bool {
	if err := yaml.Unmarshal(doc, obj); err != nil {
		s.report(Warning, name, n, fmt.Sprintf("%s does not decode: %v", kind, err))
		return false
	}
	if obj.GetName() == "" {
		s.report(Warning, name, n, kind+" has no metadata.name")
		return false
	}
	return true
}

func (s *scan) addBinding(name string, n int, binding apisv1alpha2.APIBinding) {
	export := binding.Spec.Reference.Export
	if export == nil {
		s.report(Warning, name, n, "APIBinding names no export")
		return
	}
	s.model.APIBindings = append(s.model.APIBindings, APIBinding{
		Path:   name,
		Doc:    n,
		Name:   binding.Name,
		Export: ExportRef{Path: export.Path, Name: export.Name},
	})
}

func (s *scan) report(level Level, name string, n int, reason string) {
	s.diags = append(s.diags, Diagnostic{Level: level, Path: name, Doc: n, Reason: reason})
}

func exportFrom(file string, doc int, export apisv1alpha2.APIExport) Export {
	e := Export{Path: file, Doc: doc, Name: export.Name}
	for _, r := range export.Spec.Resources {
		e.Resources = append(e.Resources, Resource{Group: r.Group, Name: r.Name, Schema: r.Schema})
	}
	for _, c := range export.Spec.PermissionClaims {
		e.Claims = append(e.Claims, Claim{Group: c.Group, Resource: c.Resource, Verbs: c.Verbs})
	}
	return e
}

func workspaceTypeFrom(file string, doc int, wt tenancyv1alpha1.WorkspaceType) WorkspaceType {
	t := WorkspaceType{Path: file, Doc: doc, Name: wt.Name}
	for _, ref := range wt.Spec.DefaultAPIBindings {
		t.Binds = append(t.Binds, ExportRef{Path: ref.Path, Name: ref.Export})
	}
	for _, ref := range wt.Spec.Extend.With {
		t.Extends = append(t.Extends, TypeRef{Path: ref.Path, Name: string(ref.Name)})
	}
	return t
}
