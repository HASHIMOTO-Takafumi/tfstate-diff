package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"reflect"
	"strings"

	"github.com/koron/go-dproxy"
	"github.com/wI2L/jsondiff"
	"gopkg.in/yaml.v2"
)

type Config struct {
	IgnoreDiff []ConfigIgnoreDiff `yaml:"ignore_diff"`
}

type ConfigIgnoreDiff struct {
	Left  string `yaml:"left"`
	Right string `yaml:"right"`
}

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
	config Config
	ps     TfProvidersSchema
}

func New(configPath string, providersSchemaPath string) Comparer {
	c := Config{}
	bytes, err := ioutil.ReadFile(configPath)
	if err != nil {
		panic(err)
	}
	if err = yaml.Unmarshal(bytes, &c); err != nil {
		panic(err)
	}

	bytes, err = ioutil.ReadFile(providersSchemaPath)
	if err != nil {
		panic(err)
	}
	var ps TfProvidersSchema
	if err = json.Unmarshal(bytes, &ps); err != nil {
		panic(err)
	}

	return Comparer{
		config: c,
		ps:     ps,
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
	address_by_id := collectAddresses(rs)

	normalizedResources := make([]TfResource, len(rs))

	for i := range rs {
		r := rs[i]

		values := replaceJsonMap("", r.Values, func(key string, value string) string {
			normalized := value
			if a, ok := address_by_id[value]; ok && a != addressNormalize(r.Address) {
				normalized = a
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

type idSource struct {
	resourceType string
	idAttribute  string
}

func collectAddresses(rs []TfResource) map[string]string {
	d := map[string]string{}

	arn_names := []string{
		"arn",
		"iam_arn",
	}

	for i := range rs {
		r := rs[i]
		for j := range arn_names {
			if val, ok := r.Values[arn_names[j]]; ok {
				if arn, ok := val.(string); ok {
					d[arn] = addressNormalize(r.Address)
				} else {
					fmt.Printf("[warn] %s.%s should be string\n", r.Address, arn_names[j])
				}
			}
		}
	}

	id_sources := []idSource{
		{
			resourceType: "aws_db_subnet_group",
			idAttribute:  "subnet_ids",
		},
		{
			resourceType: "aws_security_group",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_efs_file_system",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_vpc",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_vpc_endpoint",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_service_discovery_private_dns_namespace",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_kms_key",
			idAttribute:  "key_id",
		},
		{
			resourceType: "aws_efs_file_system",
			idAttribute:  "id",
		},
		{
			resourceType: "aws_route_table",
			idAttribute:  "id",
		},
	}

	for i := range rs {
		r := rs[i]
		for j := range id_sources {
			if r.Type != id_sources[j].resourceType {
				continue
			}
			attr := id_sources[j].idAttribute
			if val, ok := r.Values[attr]; ok {
				if id, ok := val.(string); ok {
					d[id] = fmt.Sprintf("%s.%s", addressNormalize(r.Address), attr)
				} else if ids, ok := val.([]any); ok {
					for k := range ids {
						if id, ok := ids[k].(string); ok {
							d[id] = fmt.Sprintf("%s.%s.%d", addressNormalize(r.Address), attr, j)
						} else {
							fmt.Printf("[warn] %s.%s.%d should be string\n", r.Address, attr, j)
						}
					}
				} else {
					fmt.Printf("[warn] %s.%s should be string\n", r.Address, attr)
				}
			}
		}
	}

	return d
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
				patch := compareJsons(a[i].Values, b[j].Values)
				fmt.Printf("\ncompare %s\n", a[i].Address)
				if patch != nil {
					effectiveDiffCount := 0
					for k := range patch {
						path := patch[k].Path.String()
						if c.isIgnorable(patch[k]) {
							continue
						}
						if isArgument(s, path[1:]) {
							if strings.HasSuffix(path, "/policy") || strings.HasSuffix(path, "/inline_policy") {
								c.comparePolicy(path, a[i], b[j])
							} else {
								fmt.Printf("  %s : %#v -> %#v\n", path, patch[k].OldValue, patch[k].Value)
							}
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

func (c Comparer) comparePolicy(path string, a TfResource, b TfResource) {
	v, err := dproxy.Pointer(a.Values, path).String()
	if err != nil {
		panic(err)
	}
	if v == "" {
		v = "{}"
	}

	w, err := dproxy.Pointer(b.Values, path).String()
	if err != nil {
		panic(err)
	}
	if w == "" {
		w = "{}"
	}

	fmt.Printf("  compare %s:\n", path)

	patch, err := jsondiff.CompareJSON([]byte(v), []byte(w))
	if err != nil {
		panic(err)
	}

	for i := range patch {
		path := patch[i].Path.String()

		if c.isIgnorable(patch[i]) {
			continue
		}

		fmt.Printf("    %s : %#v -> %#v\n", path, patch[i].OldValue, patch[i].Value)
	}
}

func (c Comparer) isIgnorable(op jsondiff.Operation) bool {
	path := op.Path.String()

	if strings.HasPrefix(path, "/tags/") || strings.HasPrefix(path, "/tags_all/") {
		return true
	}

	old, ok := op.OldValue.(string)
	if !ok {
		return false
	}
	new, ok := op.Value.(string)
	if !ok {
		return false
	}

	i, j := 0, 0
outer:
	for i < len(old) && j < len(new) {
		for k := range c.config.IgnoreDiff {
			ignore := c.config.IgnoreDiff[k]
			if strings.HasPrefix(old[i:], ignore.Left) && strings.HasPrefix(new[j:], ignore.Right) {
				i += len(ignore.Left)
				j += len(ignore.Right)
				continue outer
			}
		}
		if old[i] != new[j] {
			return false
		}
		i, j = i+1, j+1
	}

	return i == len(old) && j == len(new)
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

func isArgument(s TfSchema, path string) bool {
	i := strings.Index(path, "/")

	var p string
	if i < 0 {
		p = path
	} else {
		p = path[:i]
	}

	if a, ok := s.Block.Attributes[p]; ok {
		if p == "id" {
			// exception for id
			return !a.Computed
		}
		return !a.Computed || a.Optional
	}

	if a, ok := s.Block.BlockTypes[p]; ok {
		j := strings.Index(path[i+1:], "/")
		if j < 0 {
			// schema with sub-blocks should be a manual argument
			return true
		}
		return isArgument(a, path[i+1+j+1:])
	}
	panic(fmt.Errorf("schema attribute not found: %s", p))
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
