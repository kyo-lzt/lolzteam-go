package codegen

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
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
	Paths      map[string]PathItem `json:"paths"`
	Components Components          `json:"components"`
}

type Components struct {
	Schemas    map[string]SchemaObj   `json:"schemas"`
	Parameters map[string]ParamObj    `json:"parameters"`
	Responses  map[string]ResponseObj `json:"responses"`
}

type PathItem map[string]json.RawMessage // method → operation JSON

type OperationObj struct {
	OperationID string                     `json:"operationId"`
	Summary     string                     `json:"summary"`
	Description string                     `json:"description"`
	Parameters  []json.RawMessage          `json:"parameters"`
	RequestBody *RequestBodyObj            `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
}

type RequestBodyObj struct {
	Required bool                    `json:"required"`
	Content  map[string]MediaTypeObj `json:"content"`
}

type MediaTypeObj struct {
	Schema json.RawMessage `json:"schema"`
}

type ResponseObj struct {
	Content map[string]MediaTypeObj `json:"content"`
}

type ParamObj struct {
	Ref         string    `json:"$ref"`
	Name        string    `json:"name"`
	In          string    `json:"in"`
	Required    bool      `json:"required"`
	Schema      SchemaObj `json:"schema"`
	Description string    `json:"description"`
}

type SchemaObj struct {
	Ref                  string               `json:"$ref"`
	Type                 json.RawMessage      `json:"type"` // string or []string
	Properties           map[string]SchemaObj `json:"properties"`
	Items                *SchemaObj           `json:"items"`
	Required             []string             `json:"required"`
	OneOf                []SchemaObj          `json:"oneOf"`
	AnyOf                []SchemaObj          `json:"anyOf"`
	AllOf                []SchemaObj          `json:"allOf"`
	Enum                 []json.RawMessage    `json:"enum"`
	AdditionalProperties *SchemaObj           `json:"additionalProperties"`
	Description          string               `json:"description"`
	Title                string               `json:"title"`
	Default              json.RawMessage      `json:"default"`
	Format               string               `json:"format"`
}

// ---------------------------------------------------------------------------
// Parsed types used by the generator
// ---------------------------------------------------------------------------

type GoType struct {
	Name      string     // Go type expression, e.g. "int64", "ForumThreadModel"
	IsPtr     bool       // wrap in *
	StructDef *StructDef // inline struct definition, nil if named/primitive
}

type StructDef struct {
	Name   string
	Fields []StructField
}

type StructField struct {
	Name    string
	Type    GoType
	Tag     string
	Comment string
}

// ContentKind describes how the request body should be encoded.
type ContentKind int

const (
	ContentForm      ContentKind = iota // application/x-www-form-urlencoded (default)
	ContentJSON                         // application/json
	ContentMultipart                    // multipart/form-data
)

type Operation struct {
	Group          string
	Method         string
	HTTPMethod     string
	Path           string
	Summary        string
	Description    string
	ContentKind    ContentKind
	PathParams     []PathParam
	QueryParams    []QueryParam
	BodyProps      []BodyProp
	HasBinaryBody  bool
	IsArrayBody    bool       // body schema is type: "array" (e.g. batch endpoints)
	ArrayItemProps []BodyProp // properties of the array item object
	BodyUnion      *UnionDef  // discriminated union body (oneOf with discriminator)
	ResponseSchema SchemaObj
	HasResponse    bool
	IsHTMLResponse bool // response is text/html (return raw string)
}

type PathParam struct {
	Name        string
	GoName      string
	GoType      string
	SchemaObj   SchemaObj
	Description string
}

type QueryParam struct {
	Name        string
	GoName      string
	GoType      string
	Required    bool
	IsArray     bool
	ItemType    string
	Default     string // formatted default value from schema, empty if none
	Description string
}

type BodyProp struct {
	Name        string
	GoName      string
	GoType      string
	Required    bool
	IsBinary    bool
	Default     string // formatted default value from schema, empty if none
	Description string
}

// EnumDef describes a named enum type to be generated.
type EnumDef struct {
	Name   string            // Go type name, e.g. "ReplyGroup"
	GoType string            // underlying Go type, e.g. "int64" or "string"
	Values []json.RawMessage // raw enum values
}

// UnionVariant describes one variant of a discriminated union.
type UnionVariant struct {
	Name        string      // Go struct name, e.g. "OAuthTokenClientCredentials"
	Title       string      // from schema title, e.g. "Client Credentials"
	Fields      []BodyProp  // all fields including discriminator
	ContentKind ContentKind // encoding: form, json, multipart
}

// UnionDef describes a discriminated union (oneOf with single-value enum discriminator).
type UnionDef struct {
	InterfaceName string         // Go interface name, e.g. "OAuthTokenBody"
	MarkerMethod  string         // unexported marker, e.g. "oauthTokenBody"
	Variants      []UnionVariant // concrete variant structs
}

// ---------------------------------------------------------------------------
// Generator context
// ---------------------------------------------------------------------------

type Generator struct {
	Spec       OpenAPISpec
	Prefix     string // "Forum" or "Market"
	RawSpec    map[string]json.RawMessage
	Resolving  map[string]bool // cycle detection
	NamedTypes map[string]*StructDef
	EnumTypes  map[string]*EnumDef // enum type name → definition
	Operations []Operation
	Groups     map[string][]Operation
	// enumLookup maps (paramName, valuesKey) → assigned enum type name.
	// Built by CollectEnums before ParseOperations.
	enumLookup map[string]string
}

// ---------------------------------------------------------------------------
// Parsing
// ---------------------------------------------------------------------------

func (g *Generator) ParseOperations() error {
	for path, methods := range g.Spec.Paths {
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

			group, method := SplitOperationID(op.OperationID)
			// Normalize known typos in group names
			if group == "Manging" {
				group = "Managing"
			}
			parsed := Operation{
				Group:       group,
				Method:      method,
				HTTPMethod:  strings.ToUpper(httpMethod),
				Path:        path,
				Summary:     op.Summary,
				Description: op.Description,
			}

			// Parse parameters
			for _, rawParam := range op.Parameters {
				param, err := g.ResolveParam(rawParam)
				if err != nil {
					return fmt.Errorf("resolving param for %s %s: %w", httpMethod, path, err)
				}
				if param.In == "path" {
					pp := PathParam{
						Name:        param.Name,
						GoName:      ToPascalCase(param.Name),
						GoType:      g.SchemaToGoType(param.Schema),
						Description: param.Description,
					}
					parsed.PathParams = append(parsed.PathParams, pp)
				} else if param.In == "query" {
					qp := QueryParam{
						Name:        param.Name,
						GoName:      ToPascalCase(param.Name),
						Required:    param.Required,
						Description: param.Description,
					}
					resolved := g.ResolveSchemaRef(param.Schema)
					qp.Default = FormatDefault(resolved.Default)
					typeStrs := SchemaTypeStrings(resolved)
					if slices.Contains(typeStrs, "array") && resolved.Items != nil {
						qp.IsArray = true
						itemResolved := g.ResolveSchemaRef(*resolved.Items)
						if len(itemResolved.Enum) >= 2 {
							enumName := g.LookupEnum(param.Name, itemResolved.Enum)
							if enumName != "" {
								qp.ItemType = enumName
								qp.GoType = "[]" + enumName
							} else {
								qp.ItemType = g.PrimitiveGoType(itemResolved)
								qp.GoType = "[]" + qp.ItemType
							}
						} else {
							qp.ItemType = g.PrimitiveGoType(itemResolved)
							qp.GoType = "[]" + qp.ItemType
						}
					} else if slices.Contains(typeStrs, "object") && resolved.AdditionalProperties != nil {
						// deepObject params like hours_played: map[string]int64
						valType := g.PrimitiveGoType(g.ResolveSchemaRef(*resolved.AdditionalProperties))
						qp.GoType = "map[string]" + valType
					} else if len(resolved.Enum) >= 2 {
						enumName := g.LookupEnum(param.Name, resolved.Enum)
						if enumName != "" {
							qp.GoType = enumName
						} else {
							qp.GoType = g.PrimitiveGoType(resolved)
						}
					} else {
						qp.GoType = g.PrimitiveGoType(resolved)
					}
					parsed.QueryParams = append(parsed.QueryParams, qp)
				}
			}

			// Parse request body
			if op.RequestBody != nil {
				bodySchema, contentKind, err := g.ExtractBodySchema(op.RequestBody)
				if err != nil {
					return fmt.Errorf("extracting body schema for %s %s: %w", httpMethod, path, err)
				}
				parsed.ContentKind = contentKind
				isMultipart := contentKind == ContentMultipart
				resolved := g.ResolveSchemaRef(bodySchema)

				// Check if body schema is an array type (e.g. batch endpoints)
				bodyTypeStrs := SchemaTypeStrings(resolved)
				if slices.Contains(bodyTypeStrs, "array") && resolved.Items != nil {
					parsed.IsArrayBody = true
					itemResolved := g.ResolveSchemaRef(*resolved.Items)
					if len(itemResolved.AllOf) > 0 {
						itemResolved = g.MergeAllOfSchemas(itemResolved)
					}
					reqFields := ToSet(itemResolved.Required)
					for propName, propSchema := range itemResolved.Properties {
						goType := g.ResolveGoType(propSchema)
						resolvedItemProp := g.ResolveSchemaRef(propSchema)
						bp := BodyProp{
							Name:        propName,
							GoName:      ToPascalCase(propName),
							GoType:      goType,
							Required:    reqFields[propName],
							Description: resolvedItemProp.Description,
						}
						parsed.ArrayItemProps = append(parsed.ArrayItemProps, bp)
					}
					sort.Slice(parsed.ArrayItemProps, func(i, j int) bool {
						return parsed.ArrayItemProps[i].Name < parsed.ArrayItemProps[j].Name
					})
				} else if parsed.HTTPMethod == "GET" {
					// GET requests shouldn't have bodies — treat body properties as query params
					if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
						resolved = g.MergeOneOfSchemas(resolved)
					}
					if len(resolved.AllOf) > 0 {
						resolved = g.MergeAllOfSchemas(resolved)
					}
					for _, propName := range SortedKeys(resolved.Properties) {
						propSchema := resolved.Properties[propName]
						resolvedProp := g.ResolveSchemaRef(propSchema)
						qp := QueryParam{
							Name:        propName,
							GoName:      ToPascalCase(propName),
							Required:    false,
							Default:     FormatDefault(resolvedProp.Default),
							Description: resolvedProp.Description,
						}
						typeStrs := SchemaTypeStrings(resolvedProp)
						if slices.Contains(typeStrs, "array") && resolvedProp.Items != nil {
							qp.IsArray = true
							itemResolved := g.ResolveSchemaRef(*resolvedProp.Items)
							if len(itemResolved.Enum) >= 2 {
								enumName := g.LookupEnum(propName, itemResolved.Enum)
								if enumName != "" {
									qp.ItemType = enumName
									qp.GoType = "[]" + enumName
								} else {
									qp.ItemType = g.PrimitiveGoType(itemResolved)
									qp.GoType = "[]" + qp.ItemType
								}
							} else {
								qp.ItemType = g.PrimitiveGoType(itemResolved)
								qp.GoType = "[]" + qp.ItemType
							}
						} else if len(resolvedProp.Enum) >= 2 {
							enumName := g.LookupEnum(propName, resolvedProp.Enum)
							if enumName != "" {
								qp.GoType = enumName
							} else {
								qp.GoType = g.PrimitiveGoType(resolvedProp)
							}
						} else {
							qp.GoType = g.PrimitiveGoType(resolvedProp)
						}
						parsed.QueryParams = append(parsed.QueryParams, qp)
					}
				} else {
					// Check for discriminated union (oneOf with single-value enum discriminator)
					unionVariants := resolved.OneOf
					if len(unionVariants) == 0 {
						unionVariants = resolved.AnyOf
					}
					discriminator := g.DetectDiscriminatedUnion(unionVariants)
					if discriminator != "" && len(unionVariants) >= 2 {
						parsed.BodyUnion = g.BuildUnionDef(group+method, unionVariants, contentKind, isMultipart)
					} else {
						// If oneOf/anyOf, merge all properties
						if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
							resolved = g.MergeOneOfSchemas(resolved)
						}
						if len(resolved.AllOf) > 0 {
							resolved = g.MergeAllOfSchemas(resolved)
						}
						bodyStructName := group + method + "Body"
						reqFields := ToSet(resolved.Required)
						for propName, propSchema := range resolved.Properties {
							resolvedProp := g.ResolveSchemaRef(propSchema)
							isBinary := isMultipart && resolvedProp.Format == "binary"
							goType := g.ResolveGoType(propSchema)
							if isBinary {
								goType = "FileUpload"
							}
							// Inline objects with properties: generate named struct or map
							if resolvedProp.Ref == "" && len(resolvedProp.Properties) > 0 {
								if allNumericKeys(resolvedProp.Properties) {
									goType = "any" // numeric-keyed objects: API may return [] or {"id":"val"}
								} else {
									goFieldName := ToPascalCase(propName)
									inlineName := bodyStructName + goFieldName
									inlineSD := g.SchemaToStructDef(inlineName, resolvedProp)
									g.NamedTypes[inlineName] = inlineSD
									goType = inlineName
								}
							}
							// Enum detection for body properties
							if !isBinary && len(resolvedProp.Enum) >= 2 {
								enumName := g.LookupEnum(propName, resolvedProp.Enum)
								if enumName != "" {
									goType = enumName
								}
							}
							bp := BodyProp{
								Name:        propName,
								GoName:      ToPascalCase(propName),
								GoType:      goType,
								Required:    reqFields[propName],
								IsBinary:    isBinary,
								Default:     FormatDefault(resolvedProp.Default),
								Description: resolvedProp.Description,
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
			}

			// Parse response
			parsed.HasResponse = false
			for code, rawResp := range op.Responses {
				if code != "200" && code != "201" {
					continue
				}
				// Check for text/html response
				if g.IsHTMLResponse(rawResp) {
					parsed.IsHTMLResponse = true
					break
				}
				respSchema, err := g.ExtractResponseSchema(rawResp)
				if err != nil {
					return fmt.Errorf("extracting response schema for %s %s: %w", httpMethod, path, err)
				}
				if respSchema.Ref != "" || len(respSchema.Properties) > 0 || respSchema.Items != nil {
					parsed.ResponseSchema = respSchema
					parsed.HasResponse = true
					break
				}
			}

			g.Operations = append(g.Operations, parsed)
			g.Groups[group] = append(g.Groups[group], parsed)
		}
	}

	// Sort operations within each group for deterministic output
	for group := range g.Groups {
		sort.Slice(g.Groups[group], func(i, j int) bool {
			return g.Groups[group][i].Method < g.Groups[group][j].Method
		})
	}
	return nil
}

func (g *Generator) ResolveParam(raw json.RawMessage) (ParamObj, error) {
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := RefName(ref.Ref)
		if p, ok := g.Spec.Components.Parameters[name]; ok {
			return p, nil
		}
	}
	var param ParamObj
	if err := json.Unmarshal(raw, &param); err != nil {
		return ParamObj{}, fmt.Errorf("unmarshaling parameter: %w", err)
	}
	return param, nil
}

func (g *Generator) ExtractBodySchema(rb *RequestBodyObj) (SchemaObj, ContentKind, error) {
	_, hasForm := rb.Content["application/x-www-form-urlencoded"]
	_, hasMultipart := rb.Content["multipart/form-data"]
	_, hasJSON := rb.Content["application/json"]

	// Priority: multipart (without form) > json (without form) > form
	var picked string
	var kind ContentKind
	switch {
	case hasMultipart && !hasForm:
		picked = "multipart/form-data"
		kind = ContentMultipart
	case hasJSON && !hasForm:
		picked = "application/json"
		kind = ContentJSON
	case hasForm:
		picked = "application/x-www-form-urlencoded"
		kind = ContentForm
	case hasMultipart:
		picked = "multipart/form-data"
		kind = ContentMultipart
	case hasJSON:
		picked = "application/json"
		kind = ContentJSON
	default:
		return SchemaObj{}, ContentForm, nil
	}

	mt := rb.Content[picked]
	var schema SchemaObj
	if err := json.Unmarshal(mt.Schema, &schema); err != nil {
		return SchemaObj{}, ContentForm, fmt.Errorf("unmarshaling body schema (%s): %w", picked, err)
	}
	return schema, kind, nil
}

func (g *Generator) ExtractResponseSchema(raw json.RawMessage) (SchemaObj, error) {
	// Check if it's a $ref
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := RefName(ref.Ref)
		if resp, ok := g.Spec.Components.Responses[name]; ok {
			return g.ExtractResponseSchemaFromObj(resp)
		}
	}
	var resp ResponseObj
	if err := json.Unmarshal(raw, &resp); err != nil {
		return SchemaObj{}, fmt.Errorf("unmarshaling response: %w", err)
	}
	return g.ExtractResponseSchemaFromObj(resp)
}

// IsHTMLResponse checks if a raw response object contains a text/html content type.
func (g *Generator) IsHTMLResponse(raw json.RawMessage) bool {
	// Try as $ref first
	var ref struct {
		Ref string `json:"$ref"`
	}
	if err := json.Unmarshal(raw, &ref); err == nil && ref.Ref != "" {
		name := RefName(ref.Ref)
		if resp, ok := g.Spec.Components.Responses[name]; ok {
			_, hasHTML := resp.Content["text/html"]
			return hasHTML
		}
	}
	var resp ResponseObj
	if err := json.Unmarshal(raw, &resp); err != nil {
		return false
	}
	_, hasHTML := resp.Content["text/html"]
	return hasHTML
}

func (g *Generator) ExtractResponseSchemaFromObj(resp ResponseObj) (SchemaObj, error) {
	if mt, ok := resp.Content["application/json"]; ok {
		var schema SchemaObj
		if err := json.Unmarshal(mt.Schema, &schema); err != nil {
			return SchemaObj{}, fmt.Errorf("unmarshaling response schema: %w", err)
		}
		return schema, nil
	}
	return SchemaObj{}, nil
}

func (g *Generator) ResolveSchemaRef(s SchemaObj) SchemaObj {
	if s.Ref == "" {
		return s
	}

	ref := s.Ref
	if !strings.HasPrefix(ref, "#/") {
		return s
	}

	// Standard: #/components/schemas/Name
	name := RefName(ref)
	if schema, ok := g.Spec.Components.Schemas[name]; ok {
		return schema
	}

	// Deep ref like #/components/parameters/random_proxy/schema
	// Navigate the raw JSON tree
	parts := strings.Split(strings.TrimPrefix(ref, "#/"), "/")
	var current json.RawMessage
	if raw, ok := g.RawSpec[parts[0]]; ok {
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

func (g *Generator) ResolveGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := RefName(s.Ref)
		goName := ToPascalCase(name)
		// If this is a named struct type we generated, use that name
		if _, exists := g.NamedTypes[goName]; exists {
			return goName
		}
		// Otherwise resolve and return the primitive/computed Go type
		resolved := g.ResolveSchemaRef(s)
		return g.SchemaToGoType(resolved)
	}
	resolved := g.ResolveSchemaRef(s)
	return g.SchemaToGoType(resolved)
}

func (g *Generator) SchemaToGoType(s SchemaObj) string {
	if s.Ref != "" {
		name := RefName(s.Ref)
		resolved := g.ResolveSchemaRef(s)
		// Check if it's a simple scalar type (enum, primitive)
		typeStrs := SchemaTypeStrings(resolved)
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
			sorted := make([]string, len(typeStrs))
			copy(sorted, typeStrs)
			sort.Strings(sorted)
			if sorted[0] == "integer" && sorted[1] == "string" {
				return "StringOrInt"
			}
			return "any"
		}
		return ToPascalCase(name)
	}

	typeStrs := SchemaTypeStrings(s)

	// Multi-type union like ["string", "integer"]
	if len(typeStrs) > 1 {
		sorted := make([]string, len(typeStrs))
		copy(sorted, typeStrs)
		sort.Strings(sorted)
		if len(sorted) == 2 && sorted[0] == "integer" && sorted[1] == "string" {
			return "StringOrInt"
		}
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
				itemType := g.ResolveGoType(*s.Items)
				return "[]" + itemType
			}
			return "[]any"
		case "object":
			if len(s.Properties) > 0 {
				if allNumericKeys(s.Properties) {
					return "any" // numeric-keyed objects: API may return [] or {"id":"val"}
				}
				return "any" // inline objects in type context → any
			}
			if s.AdditionalProperties != nil {
				valType := g.ResolveGoType(*s.AdditionalProperties)
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

func (g *Generator) PrimitiveGoType(s SchemaObj) string {
	typeStrs := SchemaTypeStrings(s)
	if len(typeStrs) > 1 {
		sorted := make([]string, len(typeStrs))
		copy(sorted, typeStrs)
		sort.Strings(sorted)
		if len(sorted) == 2 && sorted[0] == "integer" && sorted[1] == "string" {
			return "StringOrInt"
		}
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
		resolved := g.ResolveSchemaRef(s)
		return g.PrimitiveGoType(resolved)
	}
	return "string"
}

func (g *Generator) MergeOneOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	reqSets := []map[string]bool{}
	variants := s.OneOf
	if len(variants) == 0 {
		variants = s.AnyOf
	}
	for _, variant := range variants {
		resolved := g.ResolveSchemaRef(variant)
		if len(resolved.AllOf) > 0 {
			resolved = g.MergeAllOfSchemas(resolved)
		}
		for k, v := range resolved.Properties {
			if _, exists := merged.Properties[k]; !exists {
				merged.Properties[k] = v
			}
		}
		reqSets = append(reqSets, ToSet(resolved.Required))
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

func (g *Generator) MergeAllOfSchemas(s SchemaObj) SchemaObj {
	merged := SchemaObj{
		Properties: make(map[string]SchemaObj),
	}
	for _, part := range s.AllOf {
		resolved := g.ResolveSchemaRef(part)
		for k, v := range resolved.Properties {
			merged.Properties[k] = v
		}
		merged.Required = append(merged.Required, resolved.Required...)
	}
	return merged
}

// DetectDiscriminatedUnion checks if a oneOf schema uses a discriminator pattern:
// each variant has a property with a single-value enum. Returns the discriminator
// property name, or "" if not a discriminated union.
func (g *Generator) DetectDiscriminatedUnion(variants []SchemaObj) string {
	if len(variants) < 2 {
		return ""
	}
	// Find properties that appear in every variant with a single-value enum
	candidates := map[string]int{}
	for _, variant := range variants {
		resolved := g.ResolveSchemaRef(variant)
		if len(resolved.AllOf) > 0 {
			resolved = g.MergeAllOfSchemas(resolved)
		}
		for propName, propSchema := range resolved.Properties {
			propResolved := g.ResolveSchemaRef(propSchema)
			if len(propResolved.Enum) == 1 {
				candidates[propName]++
			}
		}
	}
	for propName, count := range candidates {
		if count == len(variants) {
			return propName
		}
	}
	return ""
}

// BuildUnionDef constructs a UnionDef from oneOf variants with a discriminator.
func (g *Generator) BuildUnionDef(baseName string, variants []SchemaObj, contentKind ContentKind, isMultipart bool) *UnionDef {
	interfaceName := baseName + "Body"
	markerMethod := CamelCase(interfaceName[:1]) + interfaceName[1:]
	// Proper camelCase: "OAuthTokenBody" → "oAuthTokenBody" — but simpler to just lowercase first char
	runes := []rune(interfaceName)
	runes[0] = unicode.ToLower(runes[0])
	markerMethod = string(runes)

	ud := &UnionDef{
		InterfaceName: interfaceName,
		MarkerMethod:  markerMethod,
	}

	for _, variant := range variants {
		resolved := g.ResolveSchemaRef(variant)
		if len(resolved.AllOf) > 0 {
			resolved = g.MergeAllOfSchemas(resolved)
		}

		// Derive variant name from title
		title := resolved.Title
		if title == "" {
			title = variant.Title
		}
		variantName := baseName + ToPascalCase(title)

		reqSet := ToSet(resolved.Required)
		var fields []BodyProp
		for _, propName := range SortedKeys(resolved.Properties) {
			propSchema := resolved.Properties[propName]
			resolvedProp := g.ResolveSchemaRef(propSchema)
			isBinary := isMultipart && resolvedProp.Format == "binary"
			goType := g.ResolveGoType(propSchema)
			if isBinary {
				goType = "FileUpload"
			}
			// Inline objects with properties: generate named struct or map
			if resolvedProp.Ref == "" && len(resolvedProp.Properties) > 0 {
				if allNumericKeys(resolvedProp.Properties) {
					goType = "any" // numeric-keyed objects: API may return [] or {"id":"val"}
				} else {
					goFieldName := ToPascalCase(propName)
					inlineName := variantName + goFieldName
					inlineSD := g.SchemaToStructDef(inlineName, resolvedProp)
					g.NamedTypes[inlineName] = inlineSD
					goType = inlineName
				}
			}
			// Enum detection for body properties
			if !isBinary && len(resolvedProp.Enum) >= 2 {
				enumName := g.LookupEnum(propName, resolvedProp.Enum)
				if enumName != "" {
					goType = enumName
				}
			}
			bp := BodyProp{
				Name:        propName,
				GoName:      ToPascalCase(propName),
				GoType:      goType,
				Required:    reqSet[propName],
				IsBinary:    isBinary,
				Default:     FormatDefault(resolvedProp.Default),
				Description: resolvedProp.Description,
			}
			fields = append(fields, bp)
		}

		ud.Variants = append(ud.Variants, UnionVariant{
			Name:        variantName,
			Title:       title,
			Fields:      fields,
			ContentKind: contentKind,
		})
	}

	return ud
}

// ---------------------------------------------------------------------------
// Named type generation from component schemas
// ---------------------------------------------------------------------------

func (g *Generator) GenerateNamedTypes() {
	// Pass 1: register all struct type names so cross-references resolve
	// regardless of map iteration order.
	for name, schema := range g.Spec.Components.Schemas {
		goName := ToPascalCase(name)
		typeStrs := SchemaTypeStrings(schema)

		if len(typeStrs) == 1 {
			switch typeStrs[0] {
			case "string", "integer", "number", "boolean":
				continue
			}
		}
		if len(typeStrs) > 1 {
			continue
		}
		if len(schema.Properties) == 0 {
			continue
		}

		g.NamedTypes[goName] = &StructDef{Name: goName} // placeholder
	}

	// Pass 2: populate struct fields (all names are now resolvable).
	for name, schema := range g.Spec.Components.Schemas {
		goName := ToPascalCase(name)
		if _, ok := g.NamedTypes[goName]; !ok {
			continue
		}
		g.NamedTypes[goName] = g.SchemaToStructDef(goName, schema)
	}
}

func (g *Generator) SchemaToStructDef(name string, s SchemaObj) *StructDef {
	if g.Resolving[name] {
		return &StructDef{Name: name}
	}
	g.Resolving[name] = true
	defer delete(g.Resolving, name)

	sd := &StructDef{Name: name}
	reqSet := ToSet(s.Required)

	propNames := SortedKeys(s.Properties)
	for _, propName := range propNames {
		propSchema := s.Properties[propName]
		goFieldName := ToPascalCase(propName)
		goType := g.ResolveGoType(propSchema)

		// Inline objects → generate as nested struct type or map
		resolved := g.ResolveSchemaRef(propSchema)
		if resolved.Ref == "" && len(resolved.Properties) > 0 {
			if allNumericKeys(resolved.Properties) {
				goType = "any" // numeric-keyed objects: API may return [] or {"id":"val"}
			} else {
				inlineName := name + goFieldName
				inlineSD := g.SchemaToStructDef(inlineName, resolved)
				g.NamedTypes[inlineName] = inlineSD
				goType = inlineName
			}
		}

		isRequired := reqSet[propName]
		var tag string
		if isRequired {
			tag = fmt.Sprintf("`json:\"%s\"`", propName)
		} else {
			tag = fmt.Sprintf("`json:\"%s,omitempty\"`", propName)
		}

		field := StructField{
			Name: goFieldName,
			Tag:  tag,
		}

		if isRequired {
			field.Type = GoType{Name: goType}
		} else {
			field.Type = GoType{Name: goType, IsPtr: IsPtrCandidate(goType)}
		}

		sd.Fields = append(sd.Fields, field)
	}
	return sd
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func SplitOperationID(opID string) (group, method string) {
	idx := strings.Index(opID, ".")
	if idx < 0 {
		return ToPascalCase(opID), opID
	}
	group = ToPascalCase(opID[:idx])
	rest := opID[idx+1:]
	// Join remaining dots: "Poll.Vote" → "PollVote"
	parts := strings.Split(rest, ".")
	var methodParts []string
	for _, p := range parts {
		methodParts = append(methodParts, ToPascalCase(p))
	}
	method = strings.Join(methodParts, "")
	return group, method
}

func ToPascalCase(s string) string {
	// Strip non-alphanumeric chars (except _ and -)
	var cleaned strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
			cleaned.WriteRune(r)
		}
	}
	s = cleaned.String()

	parts := SplitWords(s)
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

func SplitWords(s string) []string {
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

var GoKeywords = map[string]bool{
	"break": true, "case": true, "chan": true, "const": true, "continue": true,
	"default": true, "defer": true, "else": true, "fallthrough": true, "for": true,
	"func": true, "go": true, "goto": true, "if": true, "import": true,
	"interface": true, "map": true, "package": true, "range": true, "return": true,
	"select": true, "struct": true, "switch": true, "type": true, "var": true,
}

func CamelCase(s string) string {
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
	if GoKeywords[result] {
		result = result + "Val"
	}
	return result
}

func CleanComment(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Ensure it starts lowercase for godoc style after method name
	return s
}

// StripMarkdownBold removes **bold** markers from a string.
func StripMarkdownBold(s string) string {
	return strings.ReplaceAll(s, "**", "")
}

// WriteDocComment writes a multi-line GoDoc comment from description text.
// Each line is prefixed with "// " and markdown bold markers are stripped.
func WriteDocComment(b *strings.Builder, desc string, indent string) {
	desc = StripMarkdownBold(desc)
	for _, line := range strings.Split(desc, "\n") {
		line = strings.TrimRight(line, " \t")
		if line == "" {
			fmt.Fprintf(b, "%s//\n", indent)
		} else {
			fmt.Fprintf(b, "%s// %s\n", indent, line)
		}
	}
}

func RefName(ref string) string {
	parts := strings.Split(ref, "/")
	return parts[len(parts)-1]
}

func SchemaTypeStrings(s SchemaObj) []string {
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

func ToSet(ss []string) map[string]bool {
	m := make(map[string]bool, len(ss))
	for _, s := range ss {
		m[s] = true
	}
	return m
}

func SortedKeys(m map[string]SchemaObj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func SortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func IsPtrCandidate(goType string) bool {
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

func PtrWrap(goType string) string {
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") || goType == "any" {
		return goType
	}
	return "*" + goType
}

// ---------------------------------------------------------------------------
// Enum detection — two-phase: collect all occurrences, then assign names
// ---------------------------------------------------------------------------

// enumOccurrence records one place where an enum appears in the spec.
type enumOccurrence struct {
	Group     string
	Method    string
	ParamName string
	GoType    string // underlying Go type: "string" or "int64"
	Values    []json.RawMessage
	ValKey    string // canonical sorted key for dedup
}

// CollectEnums scans the entire OpenAPI spec and pre-assigns enum type names.
// Must be called before ParseOperations.
func (g *Generator) CollectEnums() {
	g.enumLookup = make(map[string]string)

	// Collect all enum occurrences
	var occs []enumOccurrence
	for _, methods := range g.Spec.Paths {
		for httpMethod, rawOp := range methods {
			if httpMethod == "parameters" {
				continue
			}
			var op OperationObj
			if err := json.Unmarshal(rawOp, &op); err != nil || op.OperationID == "" {
				continue
			}
			group, method := SplitOperationID(op.OperationID)
			if group == "Manging" {
				group = "Managing"
			}

			// Parameters
			for _, rawParam := range op.Parameters {
				param, err := g.ResolveParam(rawParam)
				if err != nil {
					continue
				}
				resolved := g.ResolveSchemaRef(param.Schema)
				g.collectEnumFromSchema(group, method, param.Name, resolved, &occs)
				// Also check array items
				typeStrs := SchemaTypeStrings(resolved)
				if slices.Contains(typeStrs, "array") && resolved.Items != nil {
					itemResolved := g.ResolveSchemaRef(*resolved.Items)
					g.collectEnumFromSchema(group, method, param.Name, itemResolved, &occs)
				}
			}

			// Request body properties
			if op.RequestBody != nil {
				bodySchema, _, err := g.ExtractBodySchema(op.RequestBody)
				if err != nil {
					continue
				}
				resolved := g.ResolveSchemaRef(bodySchema)
				if len(resolved.OneOf) > 0 || len(resolved.AnyOf) > 0 {
					resolved = g.MergeOneOfSchemas(resolved)
				}
				if len(resolved.AllOf) > 0 {
					resolved = g.MergeAllOfSchemas(resolved)
				}
				for propName, propSchema := range resolved.Properties {
					propResolved := g.ResolveSchemaRef(propSchema)
					g.collectEnumFromSchema(group, method, propName, propResolved, &occs)
				}
			}
		}
	}

	// Group by param name → unique value sets
	// paramName → valKey → []group names (sorted first group)
	type valEntry struct {
		goType string
		values []json.RawMessage
		groups []string // first group that uses this variant
	}
	byName := make(map[string]map[string]*valEntry)
	for _, occ := range occs {
		m, ok := byName[occ.ParamName]
		if !ok {
			m = make(map[string]*valEntry)
			byName[occ.ParamName] = m
		}
		if _, ok := m[occ.ValKey]; !ok {
			m[occ.ValKey] = &valEntry{
				goType: occ.GoType,
				values: occ.Values,
				groups: []string{occ.Group},
			}
		} else {
			// Track additional groups
			existing := m[occ.ValKey]
			found := false
			for _, g2 := range existing.groups {
				if g2 == occ.Group {
					found = true
					break
				}
			}
			if !found {
				existing.groups = append(existing.groups, occ.Group)
			}
		}
	}

	// Assign names
	usedNames := make(map[string]bool)
	for _, paramName := range SortedMapKeys(byName) {
		variants := byName[paramName]
		baseName := ToPascalCase(paramName)

		if len(variants) == 1 {
			// Single variant → use simple name
			for valKey, entry := range variants {
				name := baseName
				for usedNames[name] {
					name = entry.groups[0] + name
				}
				usedNames[name] = true
				g.EnumTypes[name] = &EnumDef{
					Name:   name,
					GoType: entry.goType,
					Values: entry.values,
				}
				g.enumLookup[paramName+"\x00"+valKey] = name
			}
		} else {
			// Multiple variants → prefix with first group name
			valKeys := SortedMapKeys(variants)
			for _, valKey := range valKeys {
				entry := variants[valKey]
				sort.Strings(entry.groups)
				prefix := entry.groups[0]
				name := prefix + baseName
				if usedNames[name] {
					for i := 2; ; i++ {
						candidate := fmt.Sprintf("%s%d", name, i)
						if !usedNames[candidate] {
							name = candidate
							break
						}
					}
				}
				usedNames[name] = true
				g.EnumTypes[name] = &EnumDef{
					Name:   name,
					GoType: entry.goType,
					Values: entry.values,
				}
				g.enumLookup[paramName+"\x00"+valKey] = name
			}
		}
	}
}

func (g *Generator) collectEnumFromSchema(group, method, paramName string, resolved SchemaObj, occs *[]enumOccurrence) {
	if len(resolved.Enum) < 2 {
		return
	}
	goType := g.PrimitiveGoType(resolved)
	if goType != "string" && goType != "int64" {
		return
	}
	valKey := enumValuesKey(resolved.Enum)
	*occs = append(*occs, enumOccurrence{
		Group:     group,
		Method:    method,
		ParamName: paramName,
		GoType:    goType,
		Values:    resolved.Enum,
		ValKey:    valKey,
	})
}

// LookupEnum returns the pre-assigned enum type name for a param with given enum values.
// Returns "" if no enum type was assigned.
func (g *Generator) LookupEnum(paramName string, values []json.RawMessage) string {
	if g.enumLookup == nil {
		return ""
	}
	valKey := enumValuesKey(values)
	return g.enumLookup[paramName+"\x00"+valKey]
}

func enumValuesKey(values []json.RawMessage) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

// EnumConstName generates a Go constant name for an enum value.
func EnumConstName(typeName string, raw json.RawMessage, goBaseType string) string {
	switch goBaseType {
	case "int64":
		var n json.Number
		if err := json.Unmarshal(raw, &n); err == nil {
			s := n.String()
			// Handle negative numbers
			if strings.HasPrefix(s, "-") {
				return typeName + "Neg" + s[1:]
			}
			return typeName + "V" + s
		}
	case "string":
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			if s == "" {
				return typeName + "Empty"
			}
			pascal := ToPascalCase(s)
			if pascal == "" {
				// All non-alphanumeric chars (e.g. "*")
				return typeName + "All"
			}
			return typeName + pascal
		}
	}
	return typeName + "Unknown"
}

// FormatDefault returns a human-readable string for a JSON default value.
// Returns "" if the default is nil or empty.
func FormatDefault(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	s := strings.TrimSpace(string(raw))
	if s == "null" {
		return ""
	}
	// Strings: unquote for cleaner display
	var str string
	if err := json.Unmarshal(raw, &str); err == nil {
		return fmt.Sprintf("%q", str)
	}
	// Numbers, booleans: use as-is
	return s
}

// DefaultTag returns a struct tag fragment like ` default:"value"` for the given
// formatted default string. Returns "" if there is no default.
func DefaultTag(formatted string) string {
	if formatted == "" {
		return ""
	}
	// Strip surrounding quotes from string defaults (FormatDefault wraps strings in %q)
	val := formatted
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		// Unescape the inner string for the struct tag value
		var s string
		if err := json.Unmarshal([]byte(val), &s); err == nil {
			val = s
		}
	}
	return fmt.Sprintf(` default:"%s"`, val)
}

// allNumericKeys returns true when every property name in the map is a numeric string.
// Such objects represent dynamic maps (e.g. tag IDs) and should be emitted as map[string]T.
func allNumericKeys(props map[string]SchemaObj) bool {
	if len(props) == 0 {
		return false
	}
	numRe := regexp.MustCompile(`^\d+$`)
	for k := range props {
		if !numRe.MatchString(k) {
			return false
		}
	}
	return true
}

// numericKeyValueType returns the Go type for the values in a numeric-keyed object.
// All values are expected to share the same type; the first property's type is used.
func (g *Generator) numericKeyValueType(props map[string]SchemaObj) string {
	for _, v := range props {
		return g.ResolveGoType(v)
	}
	return "any"
}

// responseIntToFloat replaces int64 with float64 in a Go type string for response models.
// The API may return floating-point values for schema-declared integers.
func responseIntToFloat(goType string) string {
	if goType == "int64" {
		return "float64"
	}
	if goType == "[]int64" {
		return "[]float64"
	}
	return goType
}

// isStructType returns true if the Go type name refers to a generated struct
// (not a primitive, slice, map, or any).
func isStructType(goType string) bool {
	switch goType {
	case "string", "int64", "float64", "bool", "any":
		return false
	}
	if strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[") || strings.HasPrefix(goType, "*") {
		return false
	}
	if strings.HasPrefix(goType, "lolzteam.") {
		return false
	}
	// Must start with uppercase letter → generated struct type
	if len(goType) > 0 && unicode.IsUpper(rune(goType[0])) {
		return true
	}
	return false
}

// isPrimitiveType returns true for Go primitive types that may receive
// mismatched JSON values from the API (e.g. string instead of number).
func isPrimitiveType(goType string) bool {
	switch goType {
	case "string", "int64", "float64", "bool":
		return true
	}
	return false
}

var PathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

func BuildPathExpr(path string, params []PathParam) string {
	if len(params) == 0 {
		return fmt.Sprintf("%q", path)
	}

	paramMap := make(map[string]PathParam)
	for _, p := range params {
		paramMap[p.Name] = p
	}

	// Build fmt.Sprintf expression
	fmtStr := PathParamRe.ReplaceAllStringFunc(path, func(match string) string {
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
	matches := PathParamRe.FindAllString(path, -1)
	for _, match := range matches {
		paramName := match[1 : len(match)-1]
		p, ok := paramMap[paramName]
		if !ok {
			continue
		}
		args = append(args, CamelCase(p.GoName))
	}

	return fmt.Sprintf("fmt.Sprintf(%q, %s)", fmtStr, strings.Join(args, ", "))
}

// rootPkgTypes are types defined in the root lolzteam package that need qualified references.
var rootPkgTypes = map[string]bool{"FileUpload": true, "StringOrInt": true}

// WriteEnumDef writes a named type and const block for an enum.
func WriteEnumDef(b *strings.Builder, ed *EnumDef) {
	fmt.Fprintf(b, "// %s is an enum type.\n", ed.Name)
	fmt.Fprintf(b, "type %s %s\n\n", ed.Name, ed.GoType)
	fmt.Fprintf(b, "const (\n")
	seen := make(map[string]bool)
	for _, raw := range ed.Values {
		constName := EnumConstName(ed.Name, raw, ed.GoType)
		if seen[constName] {
			// Append raw value suffix to disambiguate
			constName = constName + "X"
			for seen[constName] {
				constName = constName + "X"
			}
		}
		seen[constName] = true
		switch ed.GoType {
		case "int64":
			var n json.Number
			if err := json.Unmarshal(raw, &n); err == nil {
				fmt.Fprintf(b, "\t%s %s = %s\n", constName, ed.Name, n.String())
			}
		case "string":
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				fmt.Fprintf(b, "\t%s %s = %q\n", constName, ed.Name, s)
			}
		}
	}
	fmt.Fprintf(b, ")\n")
}

func WriteStructDef(b *strings.Builder, sd *StructDef) {
	fmt.Fprintf(b, "// %s represents a component schema.\n", sd.Name)
	fmt.Fprintf(b, "type %s struct {\n", sd.Name)
	for _, f := range sd.Fields {
		typeName := f.Type.Name
		// Issue 2: integer → float64 for response/component types
		typeName = responseIntToFloat(typeName)
		if rootPkgTypes[typeName] {
			typeName = "lolzteam." + typeName
		}
		// Struct-typed fields use `any` — the API may return [] instead of an object
		if isStructType(typeName) {
			typeName = "any"
			// Reset IsPtr since `any` doesn't need pointer wrapping
			f.Type.IsPtr = false
		}
		// Optional primitive fields use `any` — the API may return mismatched
		// types (e.g. string instead of number, object instead of bool).
		if f.Type.IsPtr && isPrimitiveType(typeName) {
			typeName = "any"
			f.Type.IsPtr = false
		}
		// Slice fields use `any` — the API may return an object where an
		// array is expected (e.g. {"key":"val"} instead of ["val"]).
		if strings.HasPrefix(typeName, "[]") {
			typeName = "any"
		}
		if f.Type.IsPtr {
			typeName = PtrWrap(typeName)
		}
		fmt.Fprintf(b, "\t%s %s %s\n", f.Name, typeName, f.Tag)
	}
	b.WriteString("}\n")
}

// WriteUnionDef writes a discriminated union: interface + concrete variant structs.
func WriteUnionDef(b *strings.Builder, ud *UnionDef, contentKind ContentKind) {
	// Interface
	fmt.Fprintf(b, "// %s is a discriminated union for the request body.\n", ud.InterfaceName)
	fmt.Fprintf(b, "type %s interface {\n", ud.InterfaceName)
	fmt.Fprintf(b, "\t%s()\n", ud.MarkerMethod)
	fmt.Fprintf(b, "}\n\n")

	// Variant structs
	for _, v := range ud.Variants {
		tagName := "form"
		if contentKind == ContentJSON {
			tagName = "json"
		}
		fmt.Fprintf(b, "// %s is a variant of %s.\n", v.Name, ud.InterfaceName)
		fmt.Fprintf(b, "type %s struct {\n", v.Name)
		for _, bp := range v.Fields {
			goType := bp.GoType
			if rootPkgTypes[goType] {
				goType = "lolzteam." + goType
			}
			if bp.Default != "" {
				fmt.Fprintf(b, "\t// %s - Default: %s\n", bp.GoName, bp.Default)
			}
			if bp.Required && bp.Default == "" {
				fmt.Fprintf(b, "\t%s %s `%s:\"%s\"`\n", bp.GoName, goType, tagName, bp.Name)
			} else {
				ptrType := PtrWrap(goType)
				defaultTag := DefaultTag(bp.Default)
				fmt.Fprintf(b, "\t%s %s `%s:\"%s,omitempty\"%s`\n", bp.GoName, ptrType, tagName, bp.Name, defaultTag)
			}
		}
		fmt.Fprintf(b, "}\n\n")
		// Marker method
		fmt.Fprintf(b, "func (%s) %s() {}\n\n", v.Name, ud.MarkerMethod)
	}
}

func WriteFormattedFile(path, content string) error {
	formatted, err := format.Source([]byte(content))
	if err != nil {
		// Write unformatted for debugging
		os.WriteFile(path+".unformatted", []byte(content), 0644)
		return fmt.Errorf("format %s: %w", path, err)
	}
	return os.WriteFile(path, formatted, 0644)
}
