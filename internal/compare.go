package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	// only for block_types
	NestingMode string `json:"nesting_mode,omitempty"`
}

type TfSchemaBlock struct {
	Attributes map[string]TfSchemaAttribute `json:"attributes"`
	BlockTypes map[string]TfSchema          `json:"block_types"`
}

type TfSchemaAttribute struct {
	Type     any  `json:"type"` // string or []string
	Required bool `json:"required"`
	Optional bool `json:"optional"`
	Computed bool `json:"computed"`
}

type Comparer struct {
	config Config
	ps     TfProvidersSchema
	inL    idNormalizer
	inR    idNormalizer
	sn     schematicNormalizer
}

func New(configPath string, providersSchemaPath string) Comparer {
	c := Config{}

	if configPath != "" {
		bytes, err := ioutil.ReadFile(configPath)
		if err != nil {
			panic(err)
		}
		if err = yaml.Unmarshal(bytes, &c); err != nil {
			panic(err)
		}
	}

	bytes, err := ioutil.ReadFile(providersSchemaPath)
	if err != nil {
		panic(err)
	}
	var ps TfProvidersSchema
	if err = json.Unmarshal(bytes, &ps); err != nil {
		panic(err)
	}

	sn := newSchematicNormalizer(c.IgnoreDiff, ps)

	return Comparer{
		config: c,
		ps:     ps,
		sn:     sn,
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

func (c Comparer) Compare(l string, r string) {
	stateL := loadJson(l)
	stateR := loadJson(r)
	c.inL = newIdNormalizer(stateL.Values.RootModule.Resources)
	c.inR = newIdNormalizer(stateR.Values.RootModule.Resources)
	normalizedL := normalizeResource(c.inL, c.sn, stateL.Values.RootModule.Resources)
	normalizedR := normalizeResource(c.inR, c.sn, stateR.Values.RootModule.Resources)

	c.compareResources(normalizedL, normalizedR)
}

func normalizeResource(in idNormalizer, sn schematicNormalizer, rs []TfResource) []TfResource {
	normalizedResources := make([]TfResource, len(rs))

	for i := range rs {
		r := rs[i]

		values := in.normalize(r.Values)

		nr := TfResource{
			Address:      r.Address,
			Mode:         r.Mode,
			Type:         r.Type,
			Name:         r.Name,
			ProviderName: r.ProviderName,
			Values:       values,
		}

		normalizedResources[i] = sn.normalize(nr)
	}

	return normalizedResources
}

func (c Comparer) compareResources(l []TfResource, r []TfResource) {
	aNotFound := []string{}
	bFound := map[int]bool{}

	resourceWithDiffCount := 0

	for i := range l {
		found := false
		for j := range r {
			if addressNormalize(l[i].Address) == addressNormalize(r[j].Address) {
				s := c.sn.findSchema(l[i])

				patch, err := jsondiff.CompareOpts(l[i].Values, r[j].Values, jsondiff.Equivalent())
				if err != nil {
					panic(err)
				}

				fmt.Printf("\ncompare %s\n", l[i].Address)

				if patch != nil {
					effectiveDiffCount := 0
					for k := range patch {
						path := patch[k].Path.String()
						if c.isIgnorable(patch[k]) {
							continue
						}
						if isArgument(s, path[1:]) {
							if strings.HasSuffix(path, "/policy") || strings.HasSuffix(path, "/inline_policy") || strings.HasSuffix(path, "/assume_role_policy") {
								c.comparePolicy(path, l[i], r[j])
							} else {
								fmt.Printf("  %s : %s -> %s\n", path, serialize(patch[k].OldValue), serialize(patch[k].Value))
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
			aNotFound = append(aNotFound, l[i].Address)
		}
	}

	fmt.Println("Left not compared:")
	for i := range aNotFound {
		fmt.Printf("%s\n", aNotFound[i])
	}

	fmt.Println("")
	fmt.Println("Right not compared:")
	for j := range r {
		if !bFound[j] {
			fmt.Printf("%s\n", r[j].Address)
		}
	}

	fmt.Println("")
	fmt.Printf("common resources: %d\n", len(bFound))
	fmt.Printf("with diff: %d\n", resourceWithDiffCount)
	fmt.Printf("left only resources: %d\n", len(aNotFound))
	fmt.Printf("right only resources: %d\n", len(r)-len(bFound))
}

func (c Comparer) comparePolicy(path string, l TfResource, r TfResource) {
	fmt.Printf("  compare %s:\n", path)

	v, err := dproxy.Pointer(l.Values, path).String()
	if err != nil {
		panic(err)
	}
	if v == "" {
		v = "{}"
	}

	w, err := dproxy.Pointer(r.Values, path).String()
	if err != nil {
		panic(err)
	}
	if w == "" {
		w = "{}"
	}

	var dataL map[string]any
	err = json.Unmarshal([]byte(v), &dataL)
	if err != nil {
		panic(err)
	}

	var dataR map[string]any
	err = json.Unmarshal([]byte(w), &dataR)
	if err != nil {
		panic(err)
	}

	dataL = c.inL.normalize(dataL)
	dataR = c.inR.normalize(dataR)

	patch, err := jsondiff.CompareOpts(dataL, dataR, jsondiff.Equivalent())
	if err != nil {
		panic(err)
	}

	for i := range patch {
		path := patch[i].Path.String()

		if c.isIgnorable(patch[i]) {
			continue
		}

		fmt.Printf("    %s : %s -> %s\n", path, serialize(patch[i].OldValue), serialize(patch[i].Value))
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
	panic(fmt.Errorf("schema attribute not found: %s", path))
}

func addressNormalize(address string) string {
	return strings.ReplaceAll(address, "-", "_")
}

func serialize(val any) string {
	s, err := json.Marshal(val)
	if err != nil {
		panic(err)
	}

	return string(s)
}

func loadJson(path string) TfState {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		panic(err)
	}

	var data TfState
	err = json.Unmarshal(bytes, &data)
	if err != nil {
		panic(err)
	}

	return data
}
