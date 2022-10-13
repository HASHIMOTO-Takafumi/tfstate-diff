package internal

import (
	"fmt"
	"reflect"
)

type idNormalizer struct {
	address_by_id map[string]string
}

func newIdNormalizer(rs []TfResource) idNormalizer {
	return idNormalizer{
		address_by_id: collectAddresses(rs),
	}
}

func (n idNormalizer) normalize(val map[string]any) map[string]any {
	return replaceJsonMap("", val, func(key string, value string) string {
		normalized := value
		if a, ok := n.address_by_id[value]; ok {
			normalized = a
		}
		return normalized
	})
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
			resourceType: "aws_subnet",
			idAttribute:  "id",
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
