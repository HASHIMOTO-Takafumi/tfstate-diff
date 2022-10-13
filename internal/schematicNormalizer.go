package internal

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type rewriteRule struct {
	from string
	to   string
}

type schematicNormalizer struct {
	ps  TfProvidersSchema
	rrs []rewriteRule
}

func newSchematicNormalizer(c []ConfigIgnoreDiff, ps TfProvidersSchema) schematicNormalizer {
	rrs := generateRewriteRules(c)

	return schematicNormalizer{
		ps,
		rrs,
	}
}

func generateRewriteRules(c []ConfigIgnoreDiff) []rewriteRule {
	rrs := []rewriteRule{}

	for i := range c {
		l := c[i].Left
		r := c[i].Right

		var rr rewriteRule
		if len(l) >= len(r) {
			rr = rewriteRule{from: l, to: r}
		} else {
			rr = rewriteRule{from: r, to: l}
		}
		rrs = append(rrs, rr)
	}

	return rrs
}

func (n schematicNormalizer) normalize(r TfResource) TfResource {
	s := n.findSchema(r)

	vs := transformMap(r.Values, "", func(path string, value any) any {
		if value == nil {
			return nil
		}

		if isSet(s, path) {
			if vals, ok := value.([]any); ok {
				sorted := n.sort(vals)
				return sorted
			}
			panic("invalid value with set type")
		}
		return value
	})

	return TfResource{
		Address:      r.Address,
		Mode:         r.Mode,
		Type:         r.Type,
		Name:         r.Name,
		ProviderName: r.ProviderName,
		Values:       vs,
	}
}

type sortable struct {
	value      any
	serialized string
}

func (n schematicNormalizer) sort(vs []any) []any {
	svs := make([]sortable, len(vs))

	for i := range vs {
		svs[i] = sortable{
			value:      vs[i],
			serialized: n.serializeForSort(vs[i]),
		}
	}

	sort.Slice(svs, func(i int, j int) bool {
		return strings.Compare(svs[i].serialized, svs[j].serialized) < 0
	})

	result := make([]any, len(vs))

	for i := range svs {
		result[i] = svs[i].value
	}

	return result
}

func (n schematicNormalizer) serializeForSort(v any) string {
	bytes, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	s := string(bytes)

	for i := range n.rrs {
		s = strings.ReplaceAll(s, n.rrs[i].from, n.rrs[i].to)
	}

	return s
}

type jsonTransformer func(path string, value any) any

func jsonTransform(data any, path string, t jsonTransformer) any {
	if m, ok := data.(map[string]any); ok {
		return transformMap(m, path, t)
	} else if a, ok := data.([]any); ok {
		return transformArray(a, path, t)
	}

	return t(path, data)
}

func transformMap(data map[string]any, path string, t jsonTransformer) map[string]any {
	result := map[string]any{}

	for k, v := range data {
		result[k] = jsonTransform(v, fmt.Sprintf("%s/%s", path, k), t)
	}

	return result
}

func transformArray(data []any, path string, t jsonTransformer) []any {
	result := make([]any, len(data))

	for k, v := range data {
		result[k] = jsonTransform(v, fmt.Sprintf("%s/%d", path, k), t)
	}

	return t(path, result).([]any)
}

func isSet(s TfSchema, path string) bool {
	i := strings.Index(path[1:], "/")

	var p string
	if i < 0 {
		p = path[1:]
	} else {
		p = path[1 : 1+i]
	}

	if a, ok := s.Block.Attributes[p]; ok {
		if ts, ok := a.Type.([]any); ok {
			for j := range ts {
				if ts[j] == "set" {
					// Values in an attribute has no schema
					return i < 0
				}
			}
			return false
		}
		return a.Type == "set"
	}

	if a, ok := s.Block.BlockTypes[p]; ok {
		if i < 0 {
			return a.NestingMode == "set"
		}

		j := strings.Index(path[1+i+1:], "/")
		if j >= 0 {
			i += 1 + j
		}
		return isSet(a, path[1+i:])
	}

	panic(fmt.Errorf("schema attribute not found: %s", path))
}

func (n schematicNormalizer) findSchema(r TfResource) TfSchema {
	ps := n.ps.ProviderSchema[r.ProviderName]
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
