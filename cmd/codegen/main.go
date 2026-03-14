package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ---------------------------------------------------------------------------
// OpenAPI model (only the bits we need)
// ---------------------------------------------------------------------------

type OpenAPISpec struct {
	Paths      map[string]PathItem            `json:"paths"`
	Components Components                     `json:"components"`
}

type Components struct {
	Schemas    map[string]SchemaObj   `json:"schemas"`
	Parameters map[string]ParamObj   `json:"parameters"`
	Responses  map[string]ResponseObj `json:"responses"`
}

type PathItem map[string]json.RawMessage // method → operation JSON

type OperationObj struct {
	OperationID string           `json:"operationId"`
	Summary     string           `json:"summary"`
	Description string           `json:"description"`
	Parameters  []json.RawMessage `json:"parameters"`
	RequestBody *RequestBodyObj  `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

type RequestBodyObj struct {
	Required bool                       `json:"required"`
	Content  map[string]MediaTypeObj    `json:"content"`
}

type MediaTypeObj struct {
	Schema json.RawMessage `json:"schema"`
}

type ResponseObj struct {
	Content map[string]MediaTypeObj `json:"content"`
}

type ParamObj struct {
	Ref      string     `json:"$ref"`
	Name     string     `json:"name"`
	In       string     `json:"in"`
	Required bool       `json:"required"`
	Schema   SchemaObj  `json:"schema"`
	Description string  `json:"description"`
}

type SchemaObj struct {
	Ref                  string               `json:"$ref"`
	Type                 json.RawMessage       `json:"type"` // string or []string
	Properties           map[string]SchemaObj  `json:"properties"`
	Items                *SchemaObj            `json:"items"`
	Required             []string              `json:"required"`
	OneOf                []SchemaObj           `json:"oneOf"`
	AnyOf                []SchemaObj           `json:"anyOf"`
	AllOf                []SchemaObj           `json:"allOf"`
	Enum                 []json.RawMessage     `json:"enum"`
	AdditionalProperties *SchemaObj            `json:"additionalProperties"`
	Description          string                `json:"description"`
	Title                string                `json:"title"`
	Default              json.RawMessage       `json:"default"`
	Format               string                `json:"format"`
}

// ---------------------------------------------------------------------------
// Parsed types used by the generator
// ---------------------------------------------------------------------------

type GoType struct {
	Name       string // Go type expression, e.g. "int64", "ForumThreadModel"
	IsPtr      bool   // wrap in *
	StructDef  *StructDef // inline struct definition, nil if named/primitive
}

type StructDef struct {
	Name   string
	Fields []StructField
}

type StructField struct {
	Name     string
	Type     GoType
	Tag      string
	Comment  string
}

type Operation struct {
	Group       string
	Method      string
	HTTPMethod  string
	Path        string
	Summary     string
	PathParams     []PathParam
	QueryParams    []QueryParam
	BodyProps      []BodyProp
	HasBinaryBody  bool
	IsArrayBody    bool       // body schema is type: "array" (e.g. batch endpoints)
	ArrayItemProps []BodyProp // properties of the array item object
	ResponseSchema SchemaObj
	HasResponse    bool
}

type PathParam struct {
	Name     string
	GoName   string
	GoType   string
	SchemaObj SchemaObj
}

type QueryParam struct {
	Name     string
	GoName   string
	GoType   string
	Required bool
	IsArray  bool
	ItemType string
}

type BodyProp struct {
	Name     string
	GoName   string
	GoType   string
	Required bool
	IsBinary bool
}

// ---------------------------------------------------------------------------
// Generator context
// ---------------------------------------------------------------------------

type Generator struct {
	spec        OpenAPISpec
	prefix      string // "Forum" or "Market"
	rawSpec     map[string]json.RawMessage
	resolving   map[string]bool // cycle detection
	namedTypes  map[string]*StructDef
	operations  []Operation
	groups      map[string][]Operation
}

func main() {
	outDir := "."
	if len(os.Args) > 1 {
		outDir = os.Args[1]
	}

	apis := []struct {
		schemaFile string
		prefix     string
		baseURL    string
		rpm        int
	}{
		{"schemas/forum.json", "Forum", "https://api.lolz.live", 300},
		{"schemas/market.json", "Market", "https://api.lzt.market", 120},
	}

	for _, api := range apis {
		data, err := os.ReadFile(filepath.Join(outDir, api.schemaFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading %s: %v\n", api.schemaFile, err)
			os.Exit(1)
		}

		g := &Generator{
			prefix:     api.prefix,
			resolving:  make(map[string]bool),
			namedTypes: make(map[string]*StructDef),
			groups:     make(map[string][]Operation),
		}

		if err := json.Unmarshal(data, &g.spec); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", api.schemaFile, err)
			os.Exit(1)
		}

		var rawSpec map[string]json.RawMessage
		if err := json.Unmarshal(data, &rawSpec); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing raw %s: %v\n", api.schemaFile, err)
			os.Exit(1)
		}
		g.rawSpec = rawSpec

		g.parseOperations()
		g.generateNamedTypes()

		if err := g.writeClientFile(outDir, api.baseURL, api.rpm); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing client file: %v\n", err)
			os.Exit(1)
		}

		if err := g.writeTypesFile(outDir); err != nil {
			fmt.Fprintf(os.Stderr, "Error writing types file: %v\n", err)
			os.Exit(1)
		}

		for groupName, ops := range g.groups {
			if err := g.writeServiceFile(outDir, groupName, ops); err != nil {
				fmt.Fprintf(os.Stderr, "Error writing service file for %s: %v\n", groupName, err)
				os.Exit(1)
			}
		}

		fmt.Printf("Generated %s API: %d endpoints, %d groups, %d types\n",
			api.prefix, len(g.operations), len(g.groups), len(g.namedTypes))
	}
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func (g *Generator) parseOperations() {
	for path, methods := range g.spec.Paths {
		for httpMethod, rawOp := range methods {
			if httpMethod == "parameters" {
				continue
			}

			var op OperationObj
			if err := json.Unmarshal(rawOp, &op); err != nil {
				continue
			}
			if op.OperationID == "" {
				continue
			}

			group, method := splitOperationID(op.OperationID)
			// Normalize known typos in group names
			if group == "Manging" {
				group = "Managing"
			}
			parsed := Operation{
				Group:      group,
				Method:     method,
				HTTPMethod: strings.ToUpper(httpMethod),
				Path:       path,
				Summary:    op.Summary,
			}

			// Parse parameters
			for _, rawParam := range op.Parameters {
				param := g.resolveParam(rawParam)
				if param.In == "path" {
					pp := PathParam{
						Name:   param.Name,
						GoName: toPascalCase(param.Name),
						GoType: g.schemaToGoType(param.Schema),
					}
					parsed.PathParams = append(parsed.PathParams, pp)
				} else if param.In == "query" {
					qp := QueryParam{
						Name:     param.Name,
						GoName:   toPascalCase(param.Name),
						Required: param.Required,
					}
					resolved := g.resolveSchemaRef(param.Schema)
					typeStrs := schemaTypeStrings(resolved)
					if slices.Contains(typeStrs, "array") && resolved.Items != nil {
						qp.IsArray = true
						itemResolved := g.resolveSchemaRef(*resolved.Items)
						qp.ItemType = g.primitiveGoType(itemResolved)
						qp.GoType = "[]" + qp.ItemType
					} else if slices.Contains(typeStrs, "object") && resolved.AdditionalProperties != nil {
						// deepObject params like hours_played: map[string]int64
						valType := g.primitiveGoType(g.resolveSchemaRef(*resolved.AdditionalProperties))
						qp.GoType = "map[string]" + valType
					} else {
						qp.GoType = g.primitiveGoType(resolved)
					}
					parsed.QueryParams = append(parsed.QueryParams, qp)
				}
			}

			// Parse request body
			if op.RequestBody != nil {
				bodySchema, contentType := g.extractBodySchema(op.RequestBody)
				isMultipart := contentType == "multipart/form-data"
				resolved := g.resolveSchemaRef(bodySchema)

				// Check if body schema is an array type (e.g. batch endpoints)
				bodyTypeStrs := schemaTypeStrings(resolved)
				if slices.Contains(bodyTypeStrs, "array") && resolved.Items != nil {
					parsed.IsArrayBody = true
					itemResolved := g.resolveSchemaRef(*resolved.Items)
					if len(itemResolved.AllOf) > 0 {
						itemResolved = g.mergeAllOfSchemas(itemResolved)
					}
					reqFields := toSet(itemResolved.Required)
					for propName, propSchema := range itemResolved.Properties {
						goType := g.resolveGoType(propSchema)
						bp := BodyProp{
							Name:     propName,
							GoName:   toPascalCase(propName),
							GoType:   goType,
							Required: reqFields[propName],
						}
						parsed.ArrayItemProps = append(parsed.ArrayItemProps, bp)
					}
					sort.Slice(parsed.ArrayItemProps, func(i, j int) bool {
						return parsed.ArrayItemProps[i].Name < parsed.ArrayItemProps[j].Name
					})
				} else if parsed.HTTPMethod == "GET" {
					// GET requests shouldn't have bodies — treat body properties as query params
					if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
						resolved = g.mergeOneOfSchemas(resolved)
					}
					if len(resolved.AllOf) > 0 {
						resolved = g.mergeAllOfSchemas(resolved)
					}
					for propName, propSchema := range resolved.Properties {
						resolvedProp := g.resolveSchemaRef(propSchema)
						qp := QueryParam{
							Name:     propName,
							GoName:   toPascalCase(propName),
							Required: false,
						}
						typeStrs := schemaTypeStrings(resolvedProp)
						if slices.Contains(typeStrs, "array") && resolvedProp.Items != nil {
							qp.IsArray = true
							itemResolved := g.resolveSchemaRef(*resolvedProp.Items)
							qp.ItemType = g.primitiveGoType(itemResolved)
							qp.GoType = "[]" + qp.ItemType
						} else {
							qp.GoType = g.primitiveGoType(resolvedProp)
						}
						parsed.QueryParams = append(parsed.QueryParams, qp)
					}
				} else {
					// If oneOf/anyOf, merge all properties
					if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
						resolved = g.mergeOneOfSchemas(resolved)
					}
					if len(resolved.AllOf) > 0 {
						resolved = g.mergeAllOfSchemas(resolved)
					}
					reqFields := toSet(resolved.Required)
					for propName, propSchema := range resolved.Properties {
						resolvedProp := g.resolveSchemaRef(propSchema)
						isBinary := isMultipart && resolvedProp.Format == "binary"
						goType := g.resolveGoType(propSchema)
						if isBinary {
							goType = "FileUpload"
						}
						bp := BodyProp{
							Name:     propName,
							GoName:   toPascalCase(propName),
							GoType:   goType,
							Required: reqFields[propName],
							IsBinary: isBinary,
						}
						parsed.BodyProps = append(parsed.BodyProps, bp)
						if isBinary {
							parsed.HasBinaryBody = true
						}
					}
					sort.Slice(parsed.BodyProps, func(i, j int) bool {
						return parsed.BodyProps[i].Name < parsed.BodyProps[j].Name
					})
				}
			}

			// Parse response
			parsed.HasResponse = false
			for code, rawResp := range op.Responses {
				if code != "200" && code != "201" {
					continue
				}
				respSchema := g.extractResponseSchema(rawResp)
				if respSchema.Ref != "" || len(respSchema.Properties) > 0 || respSchema.Items != nil {
					parsed.ResponseSchema = respSchema
					parsed.HasResponse = true
					break
				}
			}

			g.operations = append(g.operations, parsed)
			g.groups[group] = append(g.groups[group], parsed)
		}
	}

	// Sort operations within each group for deterministic output
	for group := range g.groups {
		sort.Slice(g.groups[group], func(i, j int) bool {
			return g.groups[group][i].Method < g.groups[group][j].Method
		})
	}
}

func (g *Generator) resolveParam(raw json.RawMessage) ParamObj {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := refName(ref.Ref)
		if p, ok := g.spec.Components.Parameters[name]; ok {
			return p
		}
	}
	var param ParamObj
	json.Unmarshal(raw, &param)
	return param
}

func (g *Generator) extractBodySchema(rb *RequestBodyObj) (SchemaObj, string) {
	for _, contentType := range []string{"application/json", "multipart/form-data", "application/x-www-form-urlencoded"} {
		if mt, ok := rb.Content[contentType]; ok {
			var schema SchemaObj
			json.Unmarshal(mt.Schema, &schema)
			return schema, contentType
		}
	}
	return SchemaObj{}, ""
}

func (g *Generator) extractResponseSchema(raw json.RawMessage) SchemaObj {
	// Check if it's a $ref
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := refName(ref.Ref)
		if resp, ok := g.spec.Components.Responses[name]; ok {
			return g.extractResponseSchemaFromObj(resp)
		}
	}
	var resp ResponseObj
	json.Unmarshal(raw, &resp)
	return g.extractResponseSchemaFromObj(resp)
}

func (g *Generator) extractResponseSchemaFromObj(resp ResponseObj) SchemaObj {
	if mt, ok := resp.Content["application/json"]; ok {
		var schema SchemaObj
		json.Unmarshal(mt.Schema, &schema)
		return schema
	}
	return SchemaObj{}
}

func (g *Generator) resolveSchemaRef(s SchemaObj) SchemaObj {
	if s.Ref == "" {
		return s
	}

	ref := s.Ref
	if !strings.HasPrefix(ref, "#/") {
		return s
	}

	// Standard: #/components/schemas/Name
	name := refName(ref)
	if schema, ok := g.spec.Components.Schemas[name]; ok {
		return schema
	}

	// Deep ref like #/components/parameters/random_proxy/schema
	// Navigate the raw JSON tree
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current json.RawMessage
	if raw, ok := g.rawSpec[parts[0]]; ok {
		current = raw
	} else {
		return s
	}

	for _, part := range parts[1:] {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(current, &obj); err != nil {
			return s
		}
		next, ok := obj[part]
		if !ok {
			return s
		}
		current = next
	}

	var resolved SchemaObj
	if err := json.Unmarshal(current, &resolved); err != nil {
		return s
	}
	return resolved
}

func (g *Generator) resolveGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := refName(s.Ref)
		goName := g.prefix + toPascalCase(name)
		// If this is a named struct type we generated, use that name
		if _, exists := g.namedTypes[goName]; exists {
			return goName
		}
		// Otherwise resolve and return the primitive/computed Go type
		resolved := g.resolveSchemaRef(s)
		return g.schemaToGoType(resolved)
	}
	resolved := g.resolveSchemaRef(s)
	return g.schemaToGoType(resolved)
}

func (g *Generator) schemaToGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := refName(s.Ref)
		resolved := g.resolveSchemaRef(s)
		// Check if it's a simple scalar type (enum, primitive)
		typeStrs := schemaTypeStrings(resolved)
		if len(typeStrs) == 1 {
			t := typeStrs[0]
			switch t {
			case "string":
				return "string"
			case "integer":
				return "int64"
			case "number":
				return "float64"
			case "boolean":
				return "bool"
			}
		}
		// Multi-type union like ["string", "integer"]
		if len(typeStrs) == 2 {
			return "any"
		}
		return g.prefix + toPascalCase(name)
	}

	typeStrs := schemaTypeStrings(s)

	// Multi-type union like ["string", "integer"]
	if len(typeStrs) > 1 {
		return "any"
	}

	if len(typeStrs) == 1 {
		switch typeStrs[0] {
		case "string":
			return "string"
		case "integer":
			return "int64"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		case "array":
			if s.Items != nil {
				itemType := g.resolveGoType(*s.Items)
				return "[]" + itemType
			}
			return "[]any"
		case "object":
			if len(s.Properties) > 0 {
				return "any" // inline objects in type context → any
			}
			if s.AdditionalProperties != nil {
				valType := g.resolveGoType(*s.AdditionalProperties)
				return "map[string]" + valType
			}
			return "map[string]any"
		}
	}

	// oneOf / anyOf / allOf
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 {
		return "any"
	}
	if len(s.AllOf) > 0 {
		return "any"
	}

	// No type specified
	return "any"
}

func (g *Generator) primitiveGoType(s SchemaObj) string {
	typeStrs := schemaTypeStrings(s)
	if len(typeStrs) > 1 {
		return "any"
	}
	if len(typeStrs) == 1 {
		switch typeStrs[0] {
		case "string":
			return "string"
		case "integer":
			return "int64"
		case "number":
			return "float64"
		case "boolean":
			return "bool"
		}
	}
	if s.Ref != "" {
		resolved := g.resolveSchemaRef(s)
		return g.primitiveGoType(resolved)
	}
	return "string"
}

func (g *Generator) mergeOneOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	reqSets := []map[string]bool{}
	variants := s.OneOf
	if len(variants) == 0 {
		variants = s.AnyOf
	}
	for _, variant := range variants {
		resolved := g.resolveSchemaRef(variant)
		if len(resolved.AllOf) > 0 {
			resolved = g.mergeAllOfSchemas(resolved)
		}
		for k, v := range resolved.Properties {
			if _, exists := merged.Properties[k]; !exists {
				merged.Properties[k] = v
			}
		}
		reqSets = append(reqSets, toSet(resolved.Required))
	}
	// A field is required only if required in ALL variants
	for propName := range merged.Properties {
		allReq := true
		for _, rs := range reqSets {
			if !rs[propName] {
				allReq = false
				break
			}
		}
		if allReq {
			merged.Required = append(merged.Required, propName)
		}
	}
	return merged
}

func (g *Generator) mergeAllOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	for _, part := range s.AllOf {
		resolved := g.resolveSchemaRef(part)
		for k, v := range resolved.Properties {
			merged.Properties[k] = v
		}
		merged.Required = append(merged.Required, resolved.Required...)
	}
	return merged
}

// ---------------------------------------------------------------------------
// Named type generation from component schemas
// ---------------------------------------------------------------------------

func (g *Generator) generateNamedTypes() {
	for name, schema := range g.spec.Components.Schemas {
		goName := g.prefix + toPascalCase(name)
		typeStrs := schemaTypeStrings(schema)

		// Skip simple scalars / enums — they map to primitive Go types
		if len(typeStrs) == 1 {
			switch typeStrs[0] {
			case "string", "integer", "number", "boolean":
				continue
			}
		}
		if len(typeStrs) > 1 {
			continue // union type like ["string", "integer"]
		}

		if len(schema.Properties) == 0 {
			continue
		}

		sd := g.schemaToStructDef(goName, schema)
		g.namedTypes[goName] = sd
	}
}

func (g *Generator) schemaToStructDef(name string, s SchemaObj) *StructDef {
	if g.resolving[name] {
		return &StructDef{Name: name}
	}
	g.resolving[name] = true
	defer delete(g.resolving, name)

	sd := &StructDef{Name: name}
	reqSet := toSet(s.Required)

	propNames := sortedKeys(s.Properties)
	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := toPascalCase(propName)
		goType := g.resolveGoType(propSchema)

		// Inline objects → generate as nested struct type or map
		resolved := g.resolveSchemaRef(propSchema)
		if resolved.Ref == "" && len(resolved.Properties) > 0 {
			inlineName := name + goFieldName
			inlineSD := g.schemaToStructDef(inlineName, resolved)
			g.namedTypes[inlineName] = inlineSD
			goType = inlineName
		}

		isRequired := reqSet[propName]
		tagVal := propName + ",omitempty"
		tag := fmt.Sprintf("`json:\"%s\"`", tagVal)

		field := StructField{
			Name: goFieldName,
			Tag:  tag,
		}

		if isRequired {
			field.Type = GoType{Name: goType}
		} else {
			field.Type = GoType{Name: goType, IsPtr: isPtrCandidate(goType)}
		}

		sd.Fields = append(sd.Fields, field)
	}
	return sd
}

// ---------------------------------------------------------------------------
// Code generation — client file
// ---------------------------------------------------------------------------

func (g *Generator) writeClientFile(outDir, baseURL string, rpm int) error {
	var b strings.Builder
	b.WriteString("// Code generated by cmd/codegen. DO NOT EDIT.\n\n")
	b.WriteString("package lolzteam\n\n")

	prefix := g.prefix
	lowerPrefix := strings.ToLower(prefix)

	// Client struct
	groupNames := sortedMapKeys(g.groups)
	fmt.Fprintf(&b, "// %sClient provides access to the Lolzteam %s API.\n", prefix, prefix)
	fmt.Fprintf(&b, "type %sClient struct {\n", prefix)
	for _, gn := range groupNames {
		fmt.Fprintf(&b, "\t%s *%s%sService\n", gn, prefix, gn)
	}
	b.WriteString("}\n\n")

	// Constructor
	fmt.Fprintf(&b, "// New%sClient creates a new %s API client.\n", prefix, prefix)
	fmt.Fprintf(&b, "func New%sClient(config Config) *%sClient {\n", prefix, prefix)
	fmt.Fprintf(&b, "\tif config.BaseURL == \"\" {\n")
	fmt.Fprintf(&b, "\t\tconfig.BaseURL = %q\n", baseURL)
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\tif config.RequestsPerMinute == 0 {\n")
	fmt.Fprintf(&b, "\t\tconfig.RequestsPerMinute = %d\n", rpm)
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\tc := newHTTPClient(config)\n")
	fmt.Fprintf(&b, "\treturn &%sClient{\n", prefix)
	for _, gn := range groupNames {
		fmt.Fprintf(&b, "\t\t%s: &%s%sService{client: c},\n", gn, prefix, gn)
	}
	b.WriteString("\t}\n}\n")

	return writeFormattedFile(filepath.Join(outDir, lowerPrefix+"_client.go"), b.String())
}

// ---------------------------------------------------------------------------
// Code generation — types file
// ---------------------------------------------------------------------------

func (g *Generator) writeTypesFile(outDir string) error {
	prefix := g.prefix
	lowerPrefix := strings.ToLower(prefix)

	// Pre-pass: collect all response struct inline types into namedTypes
	// by doing a dry-run of writeResponseStruct into a throwaway buffer
	groupNames := sortedMapKeys(g.groups)
	var throwaway strings.Builder
	for _, groupName := range groupNames {
		ops := g.groups[groupName]
		for _, op := range ops {
			if op.HasResponse {
				structName := prefix + groupName + op.Method + "Response"
				g.collectResponseTypes(structName, op.ResponseSchema)
			}
		}
	}
	_ = throwaway

	var b strings.Builder
	b.WriteString("// Code generated by cmd/codegen. DO NOT EDIT.\n\n")
	b.WriteString("package lolzteam\n\n")

	// All named types (component schemas + inline response/body types)
	typeNames := sortedMapKeys(g.namedTypes)
	for _, tn := range typeNames {
		sd := g.namedTypes[tn]
		writeStructDef(&b, sd)
		b.WriteString("\n")
	}

	// Params, Body, Response structs for each operation
	for _, groupName := range groupNames {
		ops := g.groups[groupName]
		for _, op := range ops {
			// Params struct
			if len(op.QueryParams) > 0 {
				structName := prefix + groupName + op.Method + "Params"
				fmt.Fprintf(&b, "// %s holds query parameters for %s.%s.\n", structName, groupName, op.Method)
				fmt.Fprintf(&b, "type %s struct {\n", structName)
				for _, qp := range op.QueryParams {
					if qp.Required {
						fmt.Fprintf(&b, "\t%s %s `query:\"%s\"`\n", qp.GoName, qp.GoType, qp.Name)
					} else {
						ptrType := ptrWrap(qp.GoType)
						fmt.Fprintf(&b, "\t%s %s `query:\"%s\"`\n", qp.GoName, ptrType, qp.Name)
					}
				}
				b.WriteString("}\n\n")
			}

			// Body struct
			if len(op.BodyProps) > 0 {
				structName := prefix + groupName + op.Method + "Body"
				fmt.Fprintf(&b, "// %s holds the request body for %s.%s.\n", structName, groupName, op.Method)
				fmt.Fprintf(&b, "type %s struct {\n", structName)
				for _, bp := range op.BodyProps {
					if bp.Required {
						fmt.Fprintf(&b, "\t%s %s `form:\"%s\"`\n", bp.GoName, bp.GoType, bp.Name)
					} else {
						ptrType := ptrWrap(bp.GoType)
						fmt.Fprintf(&b, "\t%s %s `form:\"%s\"`\n", bp.GoName, ptrType, bp.Name)
					}
				}
				b.WriteString("}\n\n")
			}

			// Array body item struct (batch endpoints)
			if op.IsArrayBody && len(op.ArrayItemProps) > 0 {
				itemName := prefix + groupName + op.Method + "Item"
				fmt.Fprintf(&b, "// %s represents a single item in the %s.%s request body.\n", itemName, groupName, op.Method)
				fmt.Fprintf(&b, "type %s struct {\n", itemName)
				for _, bp := range op.ArrayItemProps {
					if bp.Required {
						fmt.Fprintf(&b, "\t%s %s `json:\"%s\"`\n", bp.GoName, bp.GoType, bp.Name)
					} else {
						ptrType := ptrWrap(bp.GoType)
						fmt.Fprintf(&b, "\t%s %s `json:\"%s,omitempty\"`\n", bp.GoName, ptrType, bp.Name)
					}
				}
				b.WriteString("}\n\n")
			}

			// Response struct
			if op.HasResponse {
				structName := prefix + groupName + op.Method + "Response"
				g.writeResponseStruct(&b, structName, op.ResponseSchema)
				b.WriteString("\n")
			}
		}
	}

	return writeFormattedFile(filepath.Join(outDir, lowerPrefix+"_types.go"), b.String())
}

// collectResponseTypes pre-registers all inline struct types from response schemas.
func (g *Generator) collectResponseTypes(name string, s SchemaObj) {
	resolved := g.resolveSchemaRef(s)
	if len(resolved.AllOf) > 0 {
		resolved = g.mergeAllOfSchemas(resolved)
	}
	if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
		resolved = g.mergeOneOfSchemas(resolved)
	}

	for propName, propSchema := range resolved.Properties {
		goFieldName := toPascalCase(propName)
		resolvedProp := g.resolveSchemaRef(propSchema)
		if resolvedProp.Ref == "" && len(resolvedProp.Properties) > 0 {
			inlineName := name + goFieldName
			inlineSD := g.schemaToStructDef(inlineName, resolvedProp)
			g.namedTypes[inlineName] = inlineSD
		}
	}
}

func (g *Generator) writeResponseStruct(b *strings.Builder, name string, s SchemaObj) {
	resolved := g.resolveSchemaRef(s)
	if len(resolved.AllOf) > 0 {
		resolved = g.mergeAllOfSchemas(resolved)
	}
	if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
		resolved = g.mergeOneOfSchemas(resolved)
	}

	reqSet := toSet(resolved.Required)

	fmt.Fprintf(b, "// %s is the response for the endpoint.\n", name)
	fmt.Fprintf(b, "type %s struct {\n", name)

	propNames := sortedKeys(resolved.Properties)
	for _, propName := range propNames {
		propSchema := resolved.Properties[propName]
		goFieldName := toPascalCase(propName)
		goType := g.resolveGoType(propSchema)

		// Inline objects
		resolvedProp := g.resolveSchemaRef(propSchema)
		if resolvedProp.Ref == "" && len(resolvedProp.Properties) > 0 {
			inlineName := name + goFieldName
			inlineSD := g.schemaToStructDef(inlineName, resolvedProp)
			g.namedTypes[inlineName] = inlineSD
			goType = inlineName
		}

		isRequired := reqSet[propName]
		if isRequired {
			fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", goFieldName, goType, propName)
		} else {
			ptrType := ptrWrap(goType)
			fmt.Fprintf(b, "\t%s %s `json:\"%s,omitempty\"`\n", goFieldName, ptrType, propName)
		}
	}

	b.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Code generation — service files
// ---------------------------------------------------------------------------

func (g *Generator) writeServiceFile(outDir, groupName string, ops []Operation) error {
	var b strings.Builder
	prefix := g.prefix
	lowerPrefix := strings.ToLower(prefix)

	b.WriteString("// Code generated by cmd/codegen. DO NOT EDIT.\n\n")
	b.WriteString("package lolzteam\n\n")

	needsFmt := false
	for _, op := range ops {
		if len(op.PathParams) > 0 {
			needsFmt = true
			break
		}
	}

	b.WriteString("import (\n")
	b.WriteString("\t\"context\"\n")
	if needsFmt {
		b.WriteString("\t\"fmt\"\n")
	}
	b.WriteString(")\n\n")

	// Service struct
	svcName := prefix + groupName + "Service"
	fmt.Fprintf(&b, "// %s handles %s %s API endpoints.\n", svcName, prefix, groupName)
	fmt.Fprintf(&b, "type %s struct {\n", svcName)
	b.WriteString("\tclient *httpClient\n")
	b.WriteString("}\n\n")

	// Methods
	for _, op := range ops {
		g.writeMethod(&b, groupName, op)
		b.WriteString("\n")
	}

	fileName := lowerPrefix + "_" + toSnakeCase(groupName) + ".go"
	return writeFormattedFile(filepath.Join(outDir, fileName), b.String())
}

func (g *Generator) writeMethod(b *strings.Builder, groupName string, op Operation) {
	prefix := g.prefix
	svcName := prefix + groupName + "Service"
	paramsStructName := prefix + groupName + op.Method + "Params"
	bodyStructName := prefix + groupName + op.Method + "Body"
	responseStructName := prefix + groupName + op.Method + "Response"

	hasParams := len(op.QueryParams) > 0
	hasBody := len(op.BodyProps) > 0
	itemTypeName := prefix + groupName + op.Method + "Item"

	// Build method signature
	var sigParts []string
	sigParts = append(sigParts, "ctx context.Context")
	for _, pp := range op.PathParams {
		sigParts = append(sigParts, camelCase(pp.GoName)+" "+pp.GoType)
	}
	if hasParams {
		sigParts = append(sigParts, "params *"+paramsStructName)
	}
	if op.IsArrayBody {
		sigParts = append(sigParts, "jobs []"+itemTypeName)
	} else if hasBody {
		sigParts = append(sigParts, "body *"+bodyStructName)
	}

	returnType := "error"
	if op.HasResponse {
		returnType = fmt.Sprintf("(*%s, error)", responseStructName)
	}

	// Comment
	summary := op.Summary
	if summary == "" {
		summary = op.Method
	}
	fmt.Fprintf(b, "// %s %s\n", op.Method, cleanComment(summary))

	fmt.Fprintf(b, "func (s *%s) %s(%s) %s {\n",
		svcName, op.Method, strings.Join(sigParts, ", "), returnType)

	// Result variable
	if op.HasResponse {
		fmt.Fprintf(b, "\tvar result %s\n", responseStructName)
	}

	// Build path
	pathExpr := buildPathExpr(op.Path, op.PathParams)

	fmt.Fprintf(b, "\topts := requestOptions{\n")
	fmt.Fprintf(b, "\t\tMethod: %q,\n", op.HTTPMethod)
	fmt.Fprintf(b, "\t\tPath:   %s,\n", pathExpr)
	if op.IsArrayBody {
		b.WriteString("\t\tRawJSON: jobs,\n")
	}
	b.WriteString("\t}\n")

	if hasParams {
		b.WriteString("\tif params != nil {\n")
		b.WriteString("\t\topts.Query = structToQuery(params)\n")
		b.WriteString("\t}\n")
	}

	if !op.IsArrayBody && hasBody {
		b.WriteString("\tif body != nil {\n")
		if op.HasBinaryBody {
			b.WriteString("\t\topts.Multipart = structToMultipart(body)\n")
		} else {
			b.WriteString("\t\topts.Body = structToForm(body)\n")
		}
		b.WriteString("\t}\n")
	}

	if op.HasResponse {
		b.WriteString("\tif err := s.client.request(ctx, opts, &result); err != nil {\n")
		b.WriteString("\t\treturn nil, err\n")
		b.WriteString("\t}\n")
		b.WriteString("\treturn &result, nil\n")
	} else {
		b.WriteString("\treturn s.client.request(ctx, opts, nil)\n")
	}

	b.WriteString("}\n")
}

// ---------------------------------------------------------------------------
// Path building
// ---------------------------------------------------------------------------

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func buildPathExpr(path string, params []PathParam) string {
	if len(params) == 0 {
		return fmt.Sprintf("%q", path)
	}

	paramMap := make(map[string]PathParam)
	for _, p := range params {
		paramMap[p.Name] = p
	}

	// Build fmt.Sprintf expression
	fmtStr := pathParamRe.ReplaceAllStringFunc(path, func(match string) string {
		paramName := match[1 : len(match)-1]
		p, ok := paramMap[paramName]
		if !ok {
			return match
		}
		switch p.GoType {
		case "int64":
			return "%d"
		case "any":
			return "%v"
		default:
			return "%s"
		}
	})

	var args []string
	matches := pathParamRe.FindAllString(path, -1)
	for _, match := range matches {
		paramName := match[1 : len(match)-1]
		p, ok := paramMap[paramName]
		if !ok {
			continue
		}
		args = append(args, camelCase(p.GoName))
	}

	return fmt.Sprintf("fmt.Sprintf(%q, %s)", fmtStr, strings.Join(args, ", "))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func splitOperationID(opID string) (group, method string) {
	idx := strings.Index(opID, ".")
	if idx < 0 {
		return toPascalCase(opID), opID
	}
	group = toPascalCase(opID[:idx])
	rest := opID[idx+1:]
	// Join remaining dots: "Poll.Vote" → "PollVote"
	parts := strings.Split(rest, ".")
	var methodParts []string
	for _, p := range parts {
		methodParts = append(methodParts, toPascalCase(p))
	}
	method = strings.Join(methodParts, "")
	return group, method
}

func toPascalCase(s string) string {
	// Strip non-alphanumeric chars (except _ and -)
	var cleaned strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	s = cleaned.String()

	parts := splitWords(s)
	var result strings.Builder
	for _, part := range parts {
		if part == "" {
			continue
		}
		upper := strings.ToUpper(part)
		// Common acronyms
		switch upper {
		case "ID", "URL", "URI", "API", "HTML", "JSON", "XML", "CSS", "IP",
			"HTTP", "HTTPS", "SA", "SDA", "AI", "OK", "EA", "VPN", "2FA", "FA":
			result.WriteString(upper)
		default:
			runes := []rune(part)
			runes[0] = unicode.ToUpper(runes[0])
			result.WriteString(string(runes))
		}
	}
	out := result.String()
	// Ensure the identifier starts with a letter (prefix _ for numeric starts)
	if len(out) > 0 && unicode.IsDigit(rune(out[0])) {
		out = "N" + out
	}
	return out
}

func splitWords(s string) []string {
	// Split on underscores, hyphens, and camelCase boundaries
	s = strings.ReplaceAll(s, "-", "_")
	parts := strings.Split(s, "_")
	var result []string
	for _, p := range parts {
		if p == "" {
			continue
		}
		// Split camelCase
		var word strings.Builder
		for i, r := range p {
			if i > 0 && unicode.IsUpper(r) {
				result = append(result, word.String())
				word.Reset()
			}
			word.WriteRune(r)
		}
		if word.Len() > 0 {
			result = append(result, word.String())
		}
	}
	return result
}

var goKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

func camelCase(s string) string {
	if len(s) == 0 {
		return s
	}
	// Lowercase the first letter/acronym
	runes := []rune(s)
	i := 0
	for i < len(runes) && unicode.IsUpper(runes[i]) {
		i++
	}
	if i > 1 && i < len(runes) {
		i-- // keep last uppercase as start of next word
	}
	for j := 0; j < i; j++ {
		runes[j] = unicode.ToLower(runes[j])
	}
	result := string(runes)
	if goKeywords[result] {
		result = result + "Val"
	}
	return result
}

func toSnakeCase(s string) string {
	var result strings.Builder
	for i, r := range s {
		if unicode.IsUpper(r) {
			if i > 0 {
				result.WriteByte('_')
			}
			result.WriteRune(unicode.ToLower(r))
		} else {
			result.WriteRune(r)
		}
	}
	return result.String()
}

func cleanComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Ensure it starts lowercase for godoc style after method name
	return s
}

func refName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func schemaTypeStrings(s SchemaObj) []string {
	if s.Type == nil {
		return nil
	}
	// Try as string
	var single string
	if err := json.Unmarshal(s.Type, &single); err == nil {
		return []string{single}
	}
	// Try as array
	var arr []string
	if err := json.Unmarshal(s.Type, &arr); err == nil {
		return arr
	}
	return nil
}

func toSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func sortedKeys(m map[string]SchemaObj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func isPtrCandidate(goType string) bool {
	switch goType {
	case "string", "int64", "float64", "bool":
		return true
	case "any":
		return false
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") {
		return false
	}
	return true // struct types
}

func ptrWrap(goType string) string {
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") || goType == "any" {
		return goType
	}
	return "*" + goType
}

func writeStructDef(b *strings.Builder, sd *StructDef) {
	fmt.Fprintf(b, "// %s represents a component schema.\n", sd.Name)
	fmt.Fprintf(b, "type %s struct {\n", sd.Name)
	for _, f := range sd.Fields {
		typeName := f.Type.Name
		if f.Type.IsPtr {
			typeName = ptrWrap(typeName)
		}
		fmt.Fprintf(b, "\t%s %s %s\n", f.Name, typeName, f.Tag)
	}
	b.WriteString("}\n")
}

func writeFormattedFile(path, content string) error {
	formatted, err := format.Source([]byte(content))
	if err != nil {
		// Write unformatted for debugging
		os.WriteFile(path+".unformatted", []byte(content), 0644)
		return fmt.Errorf("format %s: %w", path, err)
	}
	return os.WriteFile(path, formatted, 0644)
}
