package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"

	"github.com/wI2L/jsondiff"
)

type TfProvidersSchema struct {
	FormatVersion  string                      `json:"format_version"`
	ProviderSchema map[string]TfProviderSchema `json:"provider_schemas"`
}

type TfProviderSchema struct {
	ResourceSchemas   map[string]TfSchema `json:"resource_schemas"`
	DataSourceSchemas map[string]TfSchema `json:"data_source_schemas"`
}

type TfSchema struct {
	Block TfSchemaBlock `json:"block"`
}

type TfSchemaBlock struct {
	Attributes map[string]TfSchemaAttribute `json:"attributes"`
	BlockTypes map[string]TfSchema          `json:"block_types"`
}

type TfSchemaAttribute struct {
	Required bool `json:"required"`
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
}

type Comparer struct {
	ps TfProvidersSchema
}

func New(providersSchemaPath string) Comparer {
	bytes, err := ioutil.ReadFile(providersSchemaPath)
	if err != nil {
		panic(err)
	}
	var data TfProvidersSchema
	if err = json.Unmarshal(bytes, &data); err != nil {
		panic(err)
	}

	return Comparer{
		ps: data,
	}
}

// see https://www.terraform.io/internals/json-format
type TfState struct {
	FormatVersion    string   `json:"format_version"`
	TerraformVersion string   `json:"terraform_version"`
	Values           TfValues `json:"values"`
}

type TfValues struct {
	RootModule TfRootModule `json:"root_module"`
}

type TfRootModule struct {
	Resources []TfResource `json:"resources"`
}

type TfResource struct {
	Address      string         `json:"address"`
	Mode         string         `json:"mode"`
	Type         string         `json:"type"`
	Name         string         `json:"name"`
	ProviderName string         `json:"provider_name"`
	Values       map[string]any `json:"values"`
}

func (c Comparer) Compare(a string, b string) {
	stateA := loadJson(a)
	stateB := loadJson(b)
	normalizedA := normalize(stateA.Values.RootModule.Resources)
	normalizedB := normalize(stateB.Values.RootModule.Resources)

	c.compareResources(normalizedA, normalizedB)
}

func normalize(rs []TfResource) []TfResource {
	address_by_arn := map[string]string{}

	for i := range rs {
		r := rs[i]
		if val, ok := r.Values["arn"]; ok {
			if arn, ok := val.(string); ok {
				address_by_arn[arn] = addressNormalize(r.Address)
			} else {
				fmt.Printf("[warn] %s.arn should be string\n", r.Address)
			}
		} else if val, ok := r.Values["iam_arn"]; ok {
			if arn, ok := val.(string); ok {
				address_by_arn[arn] = addressNormalize(r.Address)
			} else {
				fmt.Printf("[warn] %s.iam_arn should be string\n", r.Address)
			}
		}
	}

	normalizedResources := make([]TfResource, len(rs))

	for i := range rs {
		r := rs[i]

		values := replaceJsonMap("", r.Values, func(key string, value string) string {
			normalized := value
			if key != "arn" && key != "iam_arn" {
				if a, ok := address_by_arn[value]; ok {
					normalized = a
				}
			}
			return normalized
		})

		normalizedResources[i] = TfResource{
			Address:      r.Address,
			Mode:         r.Mode,
			Type:         r.Type,
			Name:         r.Name,
			ProviderName: r.ProviderName,
			Values:       values,
		}
	}

	return normalizedResources
}

type jsonStringReplacer func(key string, value string) string

func replaceJsonMap(prefix string, d map[string]any, fn jsonStringReplacer) map[string]any {
	res := map[string]any{}

	for i := range d {
		var p string
		if prefix == "" {
			p = i
		} else {
			p = fmt.Sprintf("%s.%s", prefix, i)
		}
		if val, ok := d[i].(string); ok {
			res[i] = fn(p, val)
		} else if val, ok := d[i].(map[string]any); ok {
			res[i] = replaceJsonMap(p, val, fn)
		} else if val, ok := d[i].([]any); ok {
			res[i] = replaceJsonSlice(p, val, fn)
		} else {
			res[i] = replaceJsonScalar(p, val, fn)
		}
	}

	return res
}

func replaceJsonSlice(prefix string, s []any, fn jsonStringReplacer) []any {
	res := make([]any, len(s))

	for i := range s {
		var p string
		if prefix == "" {
			p = fmt.Sprintf("%d", i)
		} else {
			p = fmt.Sprintf("%s.%d", prefix, i)
		}
		if val, ok := s[i].(string); ok {
			res[i] = fn(p, val)
		} else if val, ok := s[i].(map[string]any); ok {
			res[i] = replaceJsonMap(p, val, fn)
		} else if val, ok := s[i].([]any); ok {
			res[i] = replaceJsonSlice(p, val, fn)
		} else {
			res[i] = replaceJsonScalar(p, val, fn)
		}
	}

	return res
}

func replaceJsonScalar(prefix string, v any, fn jsonStringReplacer) any {
	t := reflect.TypeOf(v)
	if t == nil || reflect.ValueOf(v).IsNil() {
		return nil
	}
	k := t.Kind()
	if k == reflect.Int || k == reflect.Float64 {
		return v
	}
	fmt.Printf("[warn] unknown type: %s %#v\n", prefix, v)
	return v
}

func (c Comparer) compareResources(a []TfResource, b []TfResource) {
	aNotFound := []string{}
	bFound := map[int]bool{}

	resourceWithDiffCount := 0

	for i := range a {
		found := false
		for j := range b {
			if addressNormalize(a[i].Address) == addressNormalize(b[j].Address) {
				s := c.findSchema(a[i])
				p := compareJsons(a[i].Values, b[j].Values)
				fmt.Printf("\ncompare %s\n", a[i].Address)
				if p != nil {
					effectiveDiffCount := 0
					for k := range p {
						path := p[k].Path.String()
						if strings.HasPrefix(path, "/tags/") || strings.HasPrefix(path, "/tags_all/") {
							continue
						}
						sa := findSchemaAttribute(s, path[1:])
						if !sa.Computed {
							fmt.Printf("  %s : %#v -> %#v\n", path, p[k].OldValue, p[k].Value)
							effectiveDiffCount++
						}
					}
					if effectiveDiffCount > 0 {
						resourceWithDiffCount++
					}
				}
				found = true
				bFound[j] = true
				break
			}
		}
		if !found {
			aNotFound = append(aNotFound, a[i].Address)
		}
	}

	fmt.Println("Left not compared:")
	for i := range aNotFound {
		fmt.Printf("%s\n", aNotFound[i])
	}

	fmt.Println("")
	fmt.Println("Right not compared:")
	for j := range b {
		if !bFound[j] {
			fmt.Printf("%s\n", b[j].Address)
		}
	}

	fmt.Println("")
	fmt.Printf("common resources: %d\n", len(bFound))
	fmt.Printf("with diff: %d\n", resourceWithDiffCount)
	fmt.Printf("left only resources: %d\n", len(aNotFound))
	fmt.Printf("right only resources: %d\n", len(b)-len(bFound))
}

func (c Comparer) findSchema(r TfResource) TfSchema {
	ps := c.ps.ProviderSchema[r.ProviderName]
	if r.Mode == "data" {
		if s, ok := ps.DataSourceSchemas[r.Type]; ok {
			return s
		}
		panic(fmt.Errorf("schema not found: %s (%s)", r.Address, r.Type))
	}
	if s, ok := ps.ResourceSchemas[r.Type]; ok {
		return s
	}
	panic(fmt.Errorf("schema not found: %s (%#v)", r.Address, r))
}

func findSchemaAttribute(s TfSchema, path string) TfSchemaAttribute {
	i := strings.Index(path, "/")
	if i < 0 {
		return s.Block.Attributes[path]
	}
	return findSchemaAttribute(s.Block.BlockTypes[path[0:i]], path[i+1:])
}

func addressNormalize(address string) string {
	return strings.ReplaceAll(address, "-", "_")
}

func loadJson(path string) TfState {
	bytes, _ := ioutil.ReadFile(path)
	var data TfState
	_ = json.Unmarshal(bytes, &data)
	return data
}

func compareJsons(a any, b any) jsondiff.Patch {
	patch, err := jsondiff.Compare(a, b)
	if err != nil {
		panic(err)
	}
	return patch
}
