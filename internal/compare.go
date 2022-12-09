package internal

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"regexp"
	"strings"

	"github.com/koron/go-dproxy"
	"github.com/wI2L/jsondiff"
	"gopkg.in/yaml.v2"
)

type Config struct {
	IgnorePattern []ConfigIgnorePattern `yaml:"ignore_pattern"`
	IgnoreDiff    []ConfigIgnoreDiff    `yaml:"ignore_diff"`
}

type ConfigIgnorePattern struct {
	Address string `yaml:"address,omitempty"`
	Path    string `yaml:"path,omitempty"`
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

type IgnorePattern struct {
	address *regexp.Regexp
	path    *regexp.Regexp
}

type Comparer struct {
	config        Config
	ignorePattern []IgnorePattern
	ps            TfProvidersSchema
	inL           idNormalizer
	inR           idNormalizer
	sn            schematicNormalizer
}

func New(configPath string, providersSchemaPath string) (*Comparer, error) {
	c := Config{}

	if configPath != "" {
		bytes, err := ioutil.ReadFile(configPath)
		if err != nil {
			return nil, err
		}
		if err = yaml.Unmarshal(bytes, &c); err != nil {
			return nil, err
		}
	}

	ip := make([]IgnorePattern, len(c.IgnorePattern))
	for i := range c.IgnorePattern {
		if c.IgnorePattern[i].Address != "" {
			re, err := regexp.Compile(c.IgnorePattern[i].Address)
			if err != nil {
				return nil, err
			}
			ip[i].address = re
		}
		if c.IgnorePattern[i].Path != "" {
			re, err := regexp.Compile(c.IgnorePattern[i].Path)
			if err != nil {
				return nil, err
			}
			ip[i].path = re
		}
	}

	bytes, err := ioutil.ReadFile(providersSchemaPath)
	if err != nil {
		return nil, err
	}
	var ps TfProvidersSchema
	if err = json.Unmarshal(bytes, &ps); err != nil {
		return nil, err
	}

	sn := newSchematicNormalizer(c.IgnoreDiff, ps)

	return &Comparer{
		config:        c,
		ignorePattern: ip,
		ps:            ps,
		sn:            sn,
	}, nil
}

// see https://www.terraform.io/internals/json-format

type TfState struct {
	FormatVersion    string    `json:"format_version"`
	TerraformVersion string    `json:"terraform_version"`
	Values           *TfValues `json:"values"`
}

type TfStatePlan struct {
	TfState
	PriorState    *TfState  `json:"prior_state"`
	PlannedValues *TfValues `json:"planned_values"`
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

func (c Comparer) Compare(l string, r string) error {
	spL, err := loadJson(l)
	if err != nil {
		return err
	}

	spR, err := loadJson(r)
	if err != nil {
		return err
	}

	isPlanL := spL.PriorState != nil
	isPlanR := spR.PriorState != nil

	var valuesL, valuesR *TfValues

	if isPlanL {
		valuesL = spL.PriorState.Values
	} else {
		valuesL = spL.TfState.Values
	}

	if isPlanR {
		valuesR = spR.PriorState.Values
	} else {
		valuesR = spR.TfState.Values
	}

	diff, err := c.compareValues(*valuesL, *valuesR)
	if err != nil {
		return err
	}

	fmt.Println("")
	fmt.Printf("common resources:    %6d\n", diff.Common)
	fmt.Printf("resources with diff: %6d\n", diff.Diff)
	fmt.Printf("left only resources: %6d\n", diff.LeftOnly)
	fmt.Printf("right only resources:%6d\n", diff.RightOnly)

	if isPlanL || isPlanR {
		if isPlanL {
			valuesL = spL.PlannedValues
		}
		if isPlanR {
			valuesR = spR.PlannedValues
		}

		planDiff, err := c.compareValues(*valuesL, *valuesR)
		if err != nil {
			return err
		}

		fmt.Println("")
		fmt.Printf("common resources:    %6d (%+4d)\n", planDiff.Common, planDiff.Common-diff.Common)
		fmt.Printf("resources with diff: %6d (%+4d)\n", planDiff.Diff, planDiff.Diff-diff.Diff)
		fmt.Printf("left only resources: %6d (%+4d)\n", planDiff.LeftOnly, planDiff.LeftOnly-diff.LeftOnly)
		fmt.Printf("right only resources:%6d (%+4d)\n", planDiff.RightOnly, planDiff.RightOnly-diff.RightOnly)
	}

	return nil
}

type StateDiff struct {
	Common    int
	Diff      int
	LeftOnly  int
	RightOnly int
}

func (c Comparer) compareValues(l TfValues, r TfValues) (*StateDiff, error) {
	c.inL = newIdNormalizer(l.RootModule.Resources)
	c.inR = newIdNormalizer(r.RootModule.Resources)
	normalizedL := normalizeResource(c.inL, c.sn, l.RootModule.Resources)
	normalizedR := normalizeResource(c.inR, c.sn, r.RootModule.Resources)

	diff, err := c.compareResources(normalizedL, normalizedR)
	if err != nil {
		return nil, err
	}

	return diff, nil
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

func (c Comparer) compareResources(l []TfResource, r []TfResource) (*StateDiff, error) {
	notFoundL := []string{}
	foundR := map[int]bool{}

	resourceWithDiffCount := 0

	for i := range l {
		found := false
		for j := range r {
			if addressNormalize(l[i].Address) == addressNormalize(r[j].Address) {
				s := c.sn.findSchema(l[i])

				patch, err := jsondiff.CompareOpts(l[i].Values, r[j].Values, jsondiff.Equivalent())
				if err != nil {
					return nil, err
				}

				fmt.Printf("\ncompare %s\n", l[i].Address)

				if patch != nil {
					effectiveDiffCount := 0
					for k := range patch {
						path := patch[k].Path.String()
						if c.isIgnorable(l[i].Address, "", patch[k]) {
							continue
						}
						isArg, err := isArgument(s, path[1:])
						if err != nil {
							return nil, err
						}
						if isArg {
							if strings.HasSuffix(path, "/policy") || strings.HasSuffix(path, "/inline_policy") || strings.HasSuffix(path, "/assume_role_policy") {
								err := c.comparePolicy(path, l[i], r[j])
								if err != nil {
									return nil, err
								}
							} else {
								old, err := serialize(patch[k].OldValue)
								if err != nil {
									return nil, err
								}
								new, err := serialize(patch[k].Value)
								if err != nil {
									return nil, err
								}
								fmt.Printf("  %s : %s -> %s\n", path, old, new)
							}
							effectiveDiffCount++
						}
					}
					if effectiveDiffCount > 0 {
						resourceWithDiffCount++
					}
				}
				found = true
				foundR[j] = true
				break
			}
		}
		if !found {
			notFoundL = append(notFoundL, l[i].Address)
		}
	}

	fmt.Println("Left not compared:")
	for i := range notFoundL {
		fmt.Printf("%s\n", notFoundL[i])
	}

	fmt.Println("")
	fmt.Println("Right not compared:")
	for j := range r {
		if !foundR[j] {
			fmt.Printf("%s\n", r[j].Address)
		}
	}

	return &StateDiff{
		Common:    len(foundR),
		Diff:      resourceWithDiffCount,
		LeftOnly:  len(notFoundL),
		RightOnly: len(r) - len(foundR),
	}, nil
}

func (c Comparer) comparePolicy(path string, l TfResource, r TfResource) error {
	fmt.Printf("  compare %s:\n", path)

	v, err := dproxy.Pointer(l.Values, path).String()
	if err != nil {
		return err
	}
	if v == "" {
		v = "{}"
	}

	w, err := dproxy.Pointer(r.Values, path).String()
	if err != nil {
		return err
	}
	if w == "" {
		w = "{}"
	}

	var dataL map[string]any
	err = json.Unmarshal([]byte(v), &dataL)
	if err != nil {
		return err
	}

	var dataR map[string]any
	err = json.Unmarshal([]byte(w), &dataR)
	if err != nil {
		return err
	}

	dataL = c.inL.normalize(dataL)
	dataR = c.inR.normalize(dataR)

	patch, err := jsondiff.CompareOpts(dataL, dataR, jsondiff.Equivalent())
	if err != nil {
		return err
	}

	for i := range patch {
		p := patch[i].Path.String()

		if c.isIgnorable(l.Address, path, patch[i]) {
			continue
		}

		old, err := serialize(patch[i].OldValue)
		if err != nil {
			return err
		}
		new, err := serialize(patch[i].Value)
		if err != nil {
			return err
		}
		fmt.Printf("    %s : %s -> %s\n", p, old, new)
	}

	return nil
}

func (c Comparer) isIgnorable(address string, basePath string, op jsondiff.Operation) bool {
	path := op.Path.String()

	fullPath := basePath + path
	for i := range c.ignorePattern {
		if (c.ignorePattern[i].address == nil || c.ignorePattern[i].address.MatchString(address)) && (c.ignorePattern[i].path == nil || c.ignorePattern[i].path.MatchString(fullPath)) {
			return true
		}
	}

	if strings.HasPrefix(path, "/tags/") || strings.HasPrefix(path, "/tags_all/") {
		return true
	}

	old, ok := op.OldValue.(string)
	if !ok {
		if olds, ok := op.OldValue.([]any); ok && len(olds) == 1 {
			if old, ok = olds[0].(string); !ok {
				return false
			}
		} else {
			return false
		}
	}
	new, ok := op.Value.(string)
	if !ok {
		if news, ok := op.Value.([]any); ok && len(news) == 1 {
			if new, ok = news[0].(string); !ok {
				return false
			}
		} else {
			return false
		}
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

func isArgument(s TfSchema, path string) (bool, error) {
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
			return !a.Computed, nil
		}
		return !a.Computed || a.Optional, nil
	}

	if a, ok := s.Block.BlockTypes[p]; ok {
		j := strings.Index(path[i+1:], "/")
		if j < 0 {
			// schema with sub-blocks should be a manual argument
			return true, nil
		}
		return isArgument(a, path[i+1+j+1:])
	}
	return false, fmt.Errorf("schema attribute not found: %s", path)
}

func addressNormalize(address string) string {
	return strings.ReplaceAll(address, "-", "_")
}

func serialize(val any) (string, error) {
	s, err := json.Marshal(val)
	if err != nil {
		return "", err
	}

	return string(s), nil
}

func loadJson(path string) (*TfStatePlan, error) {
	bytes, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var data TfStatePlan
	err = json.Unmarshal(bytes, &data)
	if err != nil {
		return nil, err
	}

	return &data, nil
}
